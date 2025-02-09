// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package traces // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/transformprocessor/internal/traces"

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl/contexts/ottlspan"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl/contexts/ottlspanevent"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/transformprocessor/internal/common"
)

func SpanFunctions() map[string]ottl.Factory[ottlspan.TransformContext] {
	// No trace-only functions yet.
	return common.Functions[ottlspan.TransformContext]()
}

func SpanEventFunctions() map[string]ottl.Factory[ottlspanevent.TransformContext] {
	// No trace-only functions yet.
	return common.Functions[ottlspanevent.TransformContext]()
}
