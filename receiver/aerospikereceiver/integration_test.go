// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration
// +build integration

package aerospikereceiver

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	as "github.com/aerospike/aerospike-client-go/v6"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/receiver/receivertest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/golden"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/scraperinttest"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/pdatatest/pmetrictest"
)

type doneCheckable interface {
	IsDone() (bool, as.Error)
}

type RecordsCheckable interface {
	Results() <-chan *as.Result
}

type aeroDoneFunc func() (doneCheckable, as.Error)
type aeroRecordsFunc func() (RecordsCheckable, as.Error)

func doneWaitAndCheck(f aeroDoneFunc, t *testing.T) {
	t.Log("starting doneWaitAndCheck")
	chk, err := f()
	require.NoError(t, err)

	for res := false; !res; res, err = chk.IsDone() {
		require.NoError(t, err)
		time.Sleep(time.Second / 3)
	}

	t.Log("leaving doneWaitAndCheck")
}

func RecordsWaitAndCheck(f aeroRecordsFunc, t *testing.T) {
	t.Log("starting RecordsWaitAndCheck")
	chk, err := f()
	require.NoError(t, err)

	// consume all records
	for range chk.Results() {
	}

	t.Log("leaving RecordsWaitAndCheck")
}

func populateMetrics(t *testing.T, host *as.Host) {
	clientPolicy := as.NewClientPolicy()
	clientPolicy.Timeout = 60 * time.Second
	// minconns is used to populate the client connections metric
	clientPolicy.MinConnectionsPerNode = 50

	var c *as.Client
	var clientErr error
	require.Eventually(t, func() bool {
		c, clientErr = as.NewClientWithPolicyAndHost(clientPolicy, host)
		return clientErr == nil
	}, 2*time.Minute, 1*time.Second, "failed to populate metrics")

	ns := "test"
	set := "integration"

	pibin := "bin1"
	sibin := "bin2"

	// write 100 records to get some memory usage
	for i := 0; i < 100; i++ {
		key, err := as.NewKey(ns, set, i)
		require.NoError(t, err, "failed to create key")

		bins := as.BinMap{
			pibin: i,
			sibin: i,
		}

		err = c.Put(nil, key, bins)
		require.NoError(t, err, "failed to write record")
	}

	// register UDFs for aggregation queries
	cwd, wderr := os.Getwd()
	require.NoError(t, wderr, "can't get working directory")

	udfFile := "udf"
	udfFunc := "sum_single_bin"
	luaPath := filepath.Join(cwd, "testdata", "integration/")
	as.SetLuaPath(luaPath)

	task, err := c.RegisterUDFFromFile(nil, filepath.Join(luaPath, udfFile+".lua"), udfFile+".lua", as.LUA)
	require.NoError(t, err, "failed registering udf file")
	require.NoError(t, <-task.OnComplete(), "failed while registering udf file")

	queryPolicy := as.NewQueryPolicy()
	queryPolicyShort := as.NewQueryPolicy()
	queryPolicyShort.ShortQuery = true

	var writePolicy *as.WritePolicy

	// *** Primary Index Queries *** //

	// perform a basic primary index query

	s1 := as.NewStatement(ns, set)
	RecordsWaitAndCheck(func() (RecordsCheckable, as.Error) {
		return c.Query(queryPolicy, s1)
	}, t)

	// aggregation query on primary index
	s2 := as.NewStatement(ns, set)
	RecordsWaitAndCheck(func() (RecordsCheckable, as.Error) {
		return c.QueryAggregate(queryPolicy, s2, "/"+udfFile, udfFunc, as.StringValue(pibin))
	}, t)
	// c.QueryAggregate(queryPolicy, s2, "/"+udfFile, udfFunc, as.StringValue(pibin))

	// background udf query on primary index
	s3 := as.NewStatement(ns, set)
	doneWaitAndCheck(func() (doneCheckable, as.Error) {
		return c.ExecuteUDF(queryPolicy, s3, "/"+udfFile, udfFunc, as.StringValue(pibin))
	}, t)

	// ops query on primary index
	s4 := as.NewStatement(ns, set)
	wbin := as.NewBin(pibin, 200)
	ops := as.PutOp(wbin)
	doneWaitAndCheck(func() (doneCheckable, as.Error) {
		return c.QueryExecute(queryPolicy, writePolicy, s4, ops)
	}, t)

	// perform a basic short primary index query
	s5 := as.NewStatement(ns, set)
	RecordsWaitAndCheck(func() (RecordsCheckable, as.Error) {
		return c.Query(queryPolicyShort, s5)
	}, t)

	// *** Secondary Index Queries *** //

	// create secondary index for SI queries
	itask, err := c.CreateIndex(writePolicy, ns, set, "sitest", "bin2", as.NUMERIC)
	require.NoError(t, err, "failed to create sindex")
	require.NoError(t, <-itask.OnComplete(), "failed running create index")

	// SI filter
	filt := as.NewRangeFilter(sibin, 0, 100)

	// perform a basic secondary index query
	s6 := as.NewStatement(ns, set)
	require.NoError(t, s6.SetFilter(filt))
	RecordsWaitAndCheck(func() (RecordsCheckable, as.Error) {
		return c.Query(queryPolicy, s6)
	}, t)

	// aggregation query on secondary index
	s7 := as.NewStatement(ns, set)
	require.NoError(t, s7.SetFilter(filt))
	RecordsWaitAndCheck(func() (RecordsCheckable, as.Error) {
		return c.QueryAggregate(queryPolicy, s7, "/"+udfFile, udfFunc, as.StringValue(sibin))
	}, t)

	// background udf query on secondary index
	s8 := as.NewStatement(ns, set)
	require.NoError(t, s8.SetFilter(filt))
	doneWaitAndCheck(func() (doneCheckable, as.Error) {
		return c.ExecuteUDF(queryPolicy, s8, "/"+udfFile, udfFunc, as.StringValue(sibin))
	}, t)

	// ops query on secondary index
	s9 := as.NewStatement(ns, set)
	require.NoError(t, s9.SetFilter(filt))
	siwbin := as.NewBin("bin4", 400)
	siops := as.PutOp(siwbin)
	doneWaitAndCheck(func() (doneCheckable, as.Error) {
		return c.QueryExecute(queryPolicy, writePolicy, s9, siops)
	}, t)

	// perform a basic short secondary index query
	s10 := as.NewStatement(ns, set)
	require.NoError(t, s10.SetFilter(filt))
	RecordsWaitAndCheck(func() (RecordsCheckable, as.Error) {
		return c.Query(queryPolicyShort, s10)
	}, t)

	// *** GeoJSON *** //

	bins := []as.BinMap{
		{
			"name":     "Bike Shop",
			"demand":   17923,
			"capacity": 17,
			"coord":    as.GeoJSONValue(`{"type" : "Point", "coordinates": [13.009318762,80.003157854]}`),
		},
		{
			"name":     "Residential Block",
			"demand":   2429,
			"capacity": 2974,
			"coord":    as.GeoJSONValue(`{"type" : "Point", "coordinates": [13.00961276, 80.003422154]}`),
		},
		{
			"name":     "Restaurant",
			"demand":   49589,
			"capacity": 4231,
			"coord":    as.GeoJSONValue(`{"type" : "Point", "coordinates": [13.009318762,80.003157854]}`),
		},
		{
			"name":     "Cafe",
			"demand":   247859,
			"capacity": 26,
			"coord":    as.GeoJSONValue(`{"type" : "Point", "coordinates": [13.00961276, 80.003422154]}`),
		},
		{
			"name":     "Park",
			"demand":   247859,
			"capacity": 26,
			"coord":    as.GeoJSONValue(`{"type" : "AeroCircle", "coordinates": [[0.0, 10.0], 10]}`),
		},
	}

	geoSet := "geoset"
	for i, b := range bins {
		key, _ := as.NewKey(ns, geoSet, i)
		err = c.Put(nil, key, b)
		require.NoError(t, err, "failed to write geojson record")
	}

	// create secondary index for geo queries
	itask, err = c.CreateIndex(writePolicy, ns, geoSet, "testset_geo_index", "coord", as.GEO2DSPHERE)
	require.NoError(t, err, "failed to create sindex")
	require.NoError(t, <-itask.OnComplete(), "failed running create index")

	// run geoJSON query
	geoStm1 := as.NewStatement(ns, geoSet)
	geoFilt1 := as.NewGeoWithinRadiusFilter("coord", float64(13.009318762), float64(80.003157854), float64(50000))
	require.NoError(t, geoStm1.SetFilter(geoFilt1))
	RecordsWaitAndCheck(func() (RecordsCheckable, as.Error) {
		return c.Query(queryPolicy, geoStm1)
	}, t)
}

func TestAerospikeIntegration(t *testing.T) {
	t.Run("6.2", test6_2.run)
	t.Run("6.2-cluster", test6_2Cluster.run)
}

type testCase struct {
	name      string
	container testcontainers.ContainerRequest
	cfgMod    func(defaultCfg *Config, endpoint string)
}

var (
	test6_2 = testCase{
		name: "6.2",
		container: testcontainers.ContainerRequest{
			Image:        "aerospike:ce-6.2.0.2",
			ExposedPorts: []string{"3000/tcp"},
			WaitingFor:   wait.ForListeningPort("3000/tcp"),
		},
		cfgMod: func(defaultCfg *Config, endpoint string) {
			defaultCfg.Endpoint = endpoint
			defaultCfg.ScraperControllerSettings.CollectionInterval = 100 * time.Millisecond
		},
	}
	test6_2Cluster = testCase{
		name: "6.2",
		container: testcontainers.ContainerRequest{
			Image:        "aerospike:ce-6.2.0.2",
			ExposedPorts: []string{"3000/tcp"},
			WaitingFor:   wait.ForListeningPort("3000/tcp"),
		},
		cfgMod: func(defaultCfg *Config, endpoint string) {
			defaultCfg.Endpoint = endpoint
			defaultCfg.ScraperControllerSettings.CollectionInterval = 100 * time.Millisecond
			defaultCfg.CollectClusterMetrics = true
		},
	}
)

func (tt testCase) run(t *testing.T) {
	container, host := getContainer(t, tt.container)
	defer func() {
		require.NoError(t, container.Terminate(context.Background()))
	}()

	f := NewFactory()
	cfg := f.CreateDefaultConfig().(*Config)
	tt.cfgMod(cfg, host)

	consumer := new(consumertest.MetricsSink)
	settings := receivertest.NewNopCreateSettings()
	rcvr, err := f.CreateMetricsReceiver(context.Background(), settings, cfg, consumer)
	require.NoError(t, err, "failed creating metrics receiver")

	require.NoError(t, rcvr.Start(context.Background(), componenttest.NewNopHost()), "failed starting metrics receiver")
	defer func() {
		require.NoError(t, rcvr.Shutdown(context.Background()))
	}()

	expectedFile := filepath.Join("testdata", "integration", "expected.yaml")
	expectedMetrics, err := golden.ReadMetrics(expectedFile)
	require.NoError(t, err, "failed reading expected metrics")

	compareOpts := []pmetrictest.CompareMetricsOption{
		pmetrictest.IgnoreMetricValues(),
		pmetrictest.IgnoreResourceAttributeValue("aerospike.node.name"),
		pmetrictest.IgnoreMetricDataPointsOrder(),
		pmetrictest.IgnoreStartTimestamp(),
		pmetrictest.IgnoreTimestamp(),
	}

	require.Eventually(t, scraperinttest.EqualsLatestMetrics(expectedMetrics, consumer, compareOpts), 30*time.Second, time.Second)
}

func getContainer(t *testing.T, req testcontainers.ContainerRequest) (testcontainers.Container, string) {
	require.NoError(t, req.Validate())
	ctx := context.Background()

	container, err := testcontainers.GenericContainer(
		ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
	require.NoError(t, err)

	mappedPort, err := container.MappedPort(ctx, "3000")
	require.Nil(t, err)

	hostIP, err := container.Host(ctx)
	require.Nil(t, err)

	host := fmt.Sprintf("%s:%s", hostIP, mappedPort.Port())
	ip, portStr, err := net.SplitHostPort(host)
	require.NoError(t, err)

	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	asHost := as.NewHost(ip, port)
	populateMetrics(t, asHost)

	return container, host
}
