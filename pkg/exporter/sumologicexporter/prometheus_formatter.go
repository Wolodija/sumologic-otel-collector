// Copyright 2020, OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sumologicexporter

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

type dataPoint interface {
	Timestamp() pcommon.Timestamp
	Attributes() pcommon.Map
}

type prometheusFormatter struct {
	sanitNameRegex *regexp.Regexp
	replacer       *strings.Replacer
}

type prometheusTags string

const (
	prometheusLeTag       string = "le"
	prometheusQuantileTag string = "quantile"
	prometheusInfValue    string = "+Inf"
)

func newPrometheusFormatter() (prometheusFormatter, error) {
	sanitNameRegex, err := regexp.Compile(`[^0-9a-zA-Z\./_:\-]`)
	if err != nil {
		return prometheusFormatter{}, err
	}

	return prometheusFormatter{
		sanitNameRegex: sanitNameRegex,
		// `\`, `"` and `\n` should be escaped, everything else should be left as-is
		// see: https://github.com/prometheus/docs/blob/main/content/docs/instrumenting/exposition_formats.md#line-format
		replacer: strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`),
	}, nil
}

// PrometheusLabels returns all attributes as sanitized prometheus labels string
func (f *prometheusFormatter) tags2String(attr pcommon.Map, labels pcommon.Map) prometheusTags {
	attrsPlusLabelsLen := attr.Len() + labels.Len()
	if attrsPlusLabelsLen == 0 {
		return ""
	}

	mergedAttributes := pcommon.NewMap()
	mergedAttributes.EnsureCapacity(attrsPlusLabelsLen)

	attr.CopyTo(mergedAttributes)
	labels.Range(func(k string, v pcommon.Value) bool {
		mergedAttributes.UpsertString(k, v.StringVal())
		return true
	})
	length := mergedAttributes.Len()

	returnValue := make([]string, 0, length)
	mergedAttributes.Range(func(k string, v pcommon.Value) bool {
		key := f.sanitizeKeyBytes([]byte(k))
		value := f.sanitizeValue(v.AsString())

		returnValue = append(
			returnValue,
			formatKeyValuePair(key, value),
		)
		return true
	})

	return prometheusTags(stringsJoinAndSurround(returnValue, ",", "{", "}"))
}

func formatKeyValuePair(key []byte, value string) string {
	const (
		quoteSign = `"`
		equalSign = `=`
	)

	// Use strings.Builder and not fmt.Sprintf as it uses significantly less
	// allocations.
	sb := strings.Builder{}
	// We preallocate space for key, value, equal sign and quotes.
	sb.Grow(len(key) + len(equalSign) + 2*len(quoteSign) + len(value))
	sb.Write(key)
	sb.WriteString(equalSign)
	sb.WriteString(quoteSign)
	sb.WriteString(value)
	sb.WriteString(quoteSign)
	return sb.String()
}

// stringsJoinAndSurround joins the strings in s slice using the separator adds front
// to the front of the resulting string and back at the end.
//
// This has a benefit over using the strings.Join() of using just one strings.Buidler
// instance and hence using less allocations to produce the final string.
func stringsJoinAndSurround(s []string, separator, front, back string) string {
	switch len(s) {
	case 0:
		return ""
	case 1:
		var b strings.Builder
		b.Grow(len(s[0]) + len(front) + len(back))
		b.WriteString(front)
		b.WriteString(s[0])
		b.WriteString(back)
		return b.String()
	}

	// Count the total strings summarized length for the preallocation.
	n := len(front) + len(s[0])
	for i := 1; i < len(s); i++ {
		n += len(separator) + len(s[i])
	}
	n += len(back)

	var b strings.Builder
	// We preallocate space for all the entires in the provided slice together with
	// the separator as well as the surrounding characters.
	b.Grow(n)
	b.WriteString(front)
	b.WriteString(s[0])
	for _, s := range s[1:] {
		b.WriteString(separator)
		b.WriteString(s)
	}
	b.WriteString(back)
	return b.String()
}

// sanitizeKeyBytes returns sanitized key byte slice by replacing
// all non-allowed chars with `_`
func (f *prometheusFormatter) sanitizeKeyBytes(s []byte) []byte {
	return f.sanitNameRegex.ReplaceAll(s, []byte{'_'})
}

// sanitizeKey returns sanitized value string performing the following substitutions:
// `/` -> `//`
// `"` -> `\"`
// "\n" -> `\n`
func (f *prometheusFormatter) sanitizeValue(s string) string {
	return f.replacer.Replace(s)
}

// doubleLine builds metric based on the given arguments where value is float64
func (f *prometheusFormatter) doubleLine(name string, attributes prometheusTags, value float64, timestamp pcommon.Timestamp) string {
	return fmt.Sprintf(
		"%s%s %g %d",
		f.sanitizeKeyBytes([]byte(name)),
		attributes,
		value,
		timestamp/pcommon.Timestamp(time.Millisecond),
	)
}

// intLine builds metric based on the given arguments where value is int64
func (f *prometheusFormatter) intLine(name string, attributes prometheusTags, value int64, timestamp pcommon.Timestamp) string {
	return fmt.Sprintf(
		"%s%s %d %d",
		f.sanitizeKeyBytes([]byte(name)),
		attributes,
		value,
		timestamp/pcommon.Timestamp(time.Millisecond),
	)
}

// uintLine builds metric based on the given arguments where value is uint64
func (f *prometheusFormatter) uintLine(name string, attributes prometheusTags, value uint64, timestamp pcommon.Timestamp) string {
	return fmt.Sprintf(
		"%s%s %d %d",
		f.sanitizeKeyBytes([]byte(name)),
		attributes,
		value,
		timestamp/pcommon.Timestamp(time.Millisecond),
	)
}

// doubleValueLine returns prometheus line with given value
func (f *prometheusFormatter) doubleValueLine(name string, value float64, dp dataPoint, attributes pcommon.Map) string {
	return f.doubleLine(
		name,
		f.tags2String(attributes, dp.Attributes()),
		value,
		dp.Timestamp(),
	)
}

// uintValueLine returns prometheus line with given value
func (f *prometheusFormatter) uintValueLine(name string, value uint64, dp dataPoint, attributes pcommon.Map) string {
	return f.uintLine(
		name,
		f.tags2String(attributes, dp.Attributes()),
		value,
		dp.Timestamp(),
	)
}

// numberDataPointValueLine returns prometheus line with value from pmetric.NumberDataPoint
func (f *prometheusFormatter) numberDataPointValueLine(name string, dp pmetric.NumberDataPoint, attributes pcommon.Map) string {
	switch dp.ValueType() {
	case pmetric.MetricValueTypeDouble:
		return f.doubleValueLine(
			name,
			dp.DoubleVal(),
			dp,
			attributes,
		)
	case pmetric.MetricValueTypeInt:
		return f.intLine(
			name,
			f.tags2String(attributes, dp.Attributes()),
			dp.IntVal(),
			dp.Timestamp(),
		)
	}
	return ""
}

// sumMetric returns _sum suffixed metric name
func (f *prometheusFormatter) sumMetric(name string) string {
	return fmt.Sprintf("%s_sum", name)
}

// countMetric returns _count suffixed metric name
func (f *prometheusFormatter) countMetric(name string) string {
	return fmt.Sprintf("%s_count", name)
}

// mergeAttributes gets two pcommon.Maps and returns new which contains values from both of them
func (f *prometheusFormatter) mergeAttributes(attributes pcommon.Map, additionalAttributes pcommon.Map) pcommon.Map {
	mergedAttributes := pcommon.NewMap()
	mergedAttributes.EnsureCapacity(attributes.Len() + additionalAttributes.Len())

	attributes.CopyTo(mergedAttributes)
	additionalAttributes.Range(func(k string, v pcommon.Value) bool {
		mergedAttributes.Upsert(k, v)
		return true
	})
	return mergedAttributes
}

// doubleGauge2Strings converts DoubleGauge record to a list of strings (one per dataPoint)
func (f *prometheusFormatter) gauge2Strings(metric pmetric.Metric, attributes pcommon.Map) []string {
	dps := metric.Gauge().DataPoints()
	lines := make([]string, 0, dps.Len())

	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		line := f.numberDataPointValueLine(
			metric.Name(),
			dp,
			attributes,
		)
		lines = append(lines, line)
	}

	return lines
}

// doubleSum2Strings converts Sum record to a list of strings (one per dataPoint)
func (f *prometheusFormatter) sum2Strings(metric pmetric.Metric, attributes pcommon.Map) []string {
	dps := metric.Sum().DataPoints()
	lines := make([]string, 0, dps.Len())

	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		line := f.numberDataPointValueLine(
			metric.Name(),
			dp,
			attributes,
		)
		lines = append(lines, line)
	}

	return lines
}

// summary2Strings converts Summary record to a list of strings
// n+2 where n is number of quantiles and 2 stands for sum and count metrics per each data point
func (f *prometheusFormatter) summary2Strings(metric pmetric.Metric, attributes pcommon.Map) []string {
	dps := metric.Summary().DataPoints()
	var lines []string

	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		qs := dp.QuantileValues()
		additionalAttributes := pcommon.NewMap()
		for i := 0; i < qs.Len(); i++ {
			q := qs.At(i)
			additionalAttributes.UpsertDouble(prometheusQuantileTag, q.Quantile())

			line := f.doubleValueLine(
				metric.Name(),
				q.Value(),
				dp,
				f.mergeAttributes(attributes, additionalAttributes),
			)
			lines = append(lines, line)
		}

		line := f.doubleValueLine(
			f.sumMetric(metric.Name()),
			dp.Sum(),
			dp,
			attributes,
		)
		lines = append(lines, line)

		line = f.uintValueLine(
			f.countMetric(metric.Name()),
			dp.Count(),
			dp,
			attributes,
		)
		lines = append(lines, line)
	}
	return lines
}

// histogram2Strings converts Histogram record to a list of strings,
// (n+1) where n is number of bounds plus two for sum and count per each data point
func (f *prometheusFormatter) histogram2Strings(metric pmetric.Metric, attributes pcommon.Map) []string {
	dps := metric.Histogram().DataPoints()
	var lines []string

	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)

		explicitBounds := dp.ExplicitBounds()
		if len(explicitBounds) == 0 {
			continue
		}

		var cumulative uint64
		additionalAttributes := pcommon.NewMap()

		for i, bound := range explicitBounds {
			cumulative += dp.BucketCounts()[i]
			additionalAttributes.UpsertDouble(prometheusLeTag, bound)

			line := f.uintValueLine(
				metric.Name(),
				cumulative,
				dp,
				f.mergeAttributes(attributes, additionalAttributes),
			)
			lines = append(lines, line)
		}

		cumulative += dp.BucketCounts()[len(explicitBounds)]
		additionalAttributes.UpsertString(prometheusLeTag, prometheusInfValue)
		line := f.uintValueLine(
			metric.Name(),
			cumulative,
			dp,
			f.mergeAttributes(attributes, additionalAttributes),
		)
		lines = append(lines, line)

		line = f.doubleValueLine(
			f.sumMetric(metric.Name()),
			dp.Sum(),
			dp,
			attributes,
		)
		lines = append(lines, line)

		line = f.uintValueLine(
			f.countMetric(metric.Name()),
			dp.Count(),
			dp,
			attributes,
		)
		lines = append(lines, line)
	}

	return lines
}

// metric2String returns stringified metricPair
func (f *prometheusFormatter) metric2String(metric pmetric.Metric, attributes pcommon.Map) string {
	var lines []string

	switch metric.DataType() {
	case pmetric.MetricDataTypeGauge:
		lines = f.gauge2Strings(metric, attributes)
	case pmetric.MetricDataTypeSum:
		lines = f.sum2Strings(metric, attributes)
	case pmetric.MetricDataTypeSummary:
		lines = f.summary2Strings(metric, attributes)
	case pmetric.MetricDataTypeHistogram:
		lines = f.histogram2Strings(metric, attributes)
	}
	return strings.Join(lines, "\n")
}
