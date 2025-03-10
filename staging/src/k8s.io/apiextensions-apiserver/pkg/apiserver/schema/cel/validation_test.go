/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cel

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"k8s.io/kube-openapi/pkg/validation/strfmt"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel/model"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
)

// TestValidationExpressions tests CEL integration with custom resource values and OpenAPIv3.
func TestValidationExpressions(t *testing.T) {
	tests := []struct {
		name          string
		schema        *schema.Structural
		obj           interface{}
		oldObj        interface{}
		valid         []string
		errors        map[string]string // rule -> string that error message must contain
		costBudget    int64
		isRoot        bool
		expectSkipped bool
	}{
		// tests where val1 and val2 are equal but val3 is different
		// equality, comparisons and type specific functions
		{name: "integers",
			// 1st obj and schema args are for "self.val1" field, 2nd for "self.val2" and so on.
			obj:    objs(math.MaxInt64, math.MaxInt64, math.MaxInt32, math.MaxInt32, math.MaxInt64, math.MaxInt64),
			schema: schemas(integerType, integerType, int32Type, int32Type, int64Type, int64Type),
			valid: []string{
				ValsEqualThemselvesAndDataLiteral("self.val1", "self.val2", fmt.Sprintf("%d", math.MaxInt64)),
				ValsEqualThemselvesAndDataLiteral("self.val3", "self.val4", fmt.Sprintf("%d", math.MaxInt32)),
				ValsEqualThemselvesAndDataLiteral("self.val5", "self.val6", fmt.Sprintf("%d", math.MaxInt64)),
				"self.val1 == self.val6", // integer with no format is the same as int64
				"type(self.val1) == int",
				fmt.Sprintf("self.val3 + 1 == %d + 1", math.MaxInt32), // CEL integers are 64 bit
				"type(self.val3) == int",
				"type(self.val5) == int",
			},
			errors: map[string]string{
				"self.val1 + 1 == 0": "integer overflow",
				"self.val5 + 1 == 0": "integer overflow",
				"1 / 0 == 1 / 0":     "division by zero",
			},
		},
		{name: "numbers",
			obj:    objs(math.MaxFloat64, math.MaxFloat64, math.MaxFloat32, math.MaxFloat32, math.MaxFloat64, math.MaxFloat64, int64(1)),
			schema: schemas(numberType, numberType, floatType, floatType, doubleType, doubleType, doubleType),
			valid: []string{
				ValsEqualThemselvesAndDataLiteral("self.val1", "self.val2", fmt.Sprintf("%f", math.MaxFloat64)),
				ValsEqualThemselvesAndDataLiteral("self.val3", "self.val4", fmt.Sprintf("%f", math.MaxFloat32)),
				ValsEqualThemselvesAndDataLiteral("self.val5", "self.val6", fmt.Sprintf("%f", math.MaxFloat64)),
				"self.val1 == self.val6", // number with no format is the same as float64
				"type(self.val1) == double",
				"type(self.val3) == double",
				"type(self.val5) == double",

				// Use a int64 value with a number openAPI schema type since float representations of whole numbers
				// (e.g. 1.0, 0.0) can convert to int representations (e.g. 1, 0) in yaml to json translation, and
				// then get parsed as int64s.
				"type(self.val7) == double",
				"self.val7 == 1.0",
			},
		},
		{name: "numeric comparisons",
			obj: objs(
				int64(5),      // val1, integer type, integer value
				int64(10),     // val2, integer type, integer value
				int64(15),     // val3, integer type, integer value
				float64(10.0), // val4, number type, parsed from decimal literal
				float64(10.0), // val5, float type, parsed from decimal literal
				float64(10.0), // val6, double type, parsed from decimal literal
				int64(10),     // val7, number type, parsed from integer literal
				int64(10),     // val8, float type, parsed from integer literal
				int64(10),     // val9, double type, parsed from integer literal
			),
			schema: schemas(integerType, integerType, integerType, numberType, floatType, doubleType, numberType, floatType, doubleType),
			valid: []string{
				// xref: https://github.com/google/cel-spec/wiki/proposal-210

				// compare integers with all float types
				"double(self.val1) < self.val4",
				"double(self.val1) <= self.val4",
				"double(self.val2) <= self.val4",
				"double(self.val2) == self.val4",
				"double(self.val2) >= self.val4",
				"double(self.val3) > self.val4",
				"double(self.val3) >= self.val4",

				"self.val1 < int(self.val4)",
				"self.val2 == int(self.val4)",
				"self.val3 > int(self.val4)",

				"double(self.val1) < self.val5",
				"double(self.val2) == self.val5",
				"double(self.val3) > self.val5",

				"self.val1 < int(self.val5)",
				"self.val2 == int(self.val5)",
				"self.val3 > int(self.val5)",

				"double(self.val1) < self.val6",
				"double(self.val2) == self.val6",
				"double(self.val3) > self.val6",

				"self.val1 < int(self.val6)",
				"self.val2 == int(self.val6)",
				"self.val3 > int(self.val6)",

				// Also compare with float types backed by integer values,
				// which is how integer literals are parsed from JSON for custom resources.
				"double(self.val1) < self.val7",
				"double(self.val2) == self.val7",
				"double(self.val3) > self.val7",

				"self.val1 < int(self.val7)",
				"self.val2 == int(self.val7)",
				"self.val3 > int(self.val7)",

				"double(self.val1) < self.val8",
				"double(self.val2) == self.val8",
				"double(self.val3) > self.val8",

				"self.val1 < int(self.val8)",
				"self.val2 == int(self.val8)",
				"self.val3 > int(self.val8)",

				"double(self.val1) < self.val9",
				"double(self.val2) == self.val9",
				"double(self.val3) > self.val9",

				"self.val1 < int(self.val9)",
				"self.val2 == int(self.val9)",
				"self.val3 > int(self.val9)",

				// compare literal integers and floats
				"double(5) < 10.0",
				"double(10) == 10.0",
				"double(15) > 10.0",

				"5 < int(10.0)",
				"10 == int(10.0)",
				"15 > int(10.0)",

				// compare integers with literal floats
				"double(self.val1) < 10.0",
				"double(self.val2) == 10.0",
				"double(self.val3) > 10.0",
			},
		},
		{name: "unicode strings",
			obj:    objs("Rook takes 👑", "Rook takes 👑"),
			schema: schemas(stringType, stringType),
			valid: []string{
				ValsEqualThemselvesAndDataLiteral("self.val1", "self.val2", "'Rook takes 👑'"),
				"self.val1.startsWith('Rook')",
				"!self.val1.startsWith('knight')",
				"self.val1.contains('takes')",
				"!self.val1.contains('gives')",
				"self.val1.endsWith('👑')",
				"!self.val1.endsWith('pawn')",
				"self.val1.matches('^[^0-9]*$')",
				"!self.val1.matches('^[0-9]*$')",
				"type(self.val1) == string",
				"size(self.val1) == 12",

				// string functions (https://github.com/google/cel-go/blob/v0.9.0/ext/strings.go)
				"self.val1.charAt(3) == 'k'",
				"self.val1.indexOf('o') == 1",
				"self.val1.indexOf('o', 2) == 2",
				"self.val1.replace(' ', 'x') == 'Rookxtakesx👑'",
				"self.val1.replace(' ', 'x', 1) == 'Rookxtakes 👑'",
				"self.val1.split(' ') == ['Rook', 'takes', '👑']",
				"self.val1.split(' ', 2) == ['Rook', 'takes 👑']",
				"self.val1.substring(5) == 'takes 👑'",
				"self.val1.substring(0, 4) == 'Rook'",
				"self.val1.substring(4, 10).trim() == 'takes'",
				"self.val1.upperAscii() == 'ROOK TAKES 👑'",
				"self.val1.lowerAscii() == 'rook takes 👑'",
			},
			errors: map[string]string{
				// Invalid regex with a string constant regex pattern is compile time error
				"self.val1.matches(')')": "compile error: program instantiation failed: error parsing regexp: unexpected ): `)`",
			},
		},
		{name: "escaped strings",
			obj:    objs("l1\nl2", "l1\nl2"),
			schema: schemas(stringType, stringType),
			valid: []string{
				ValsEqualThemselvesAndDataLiteral("self.val1", "self.val2", "'l1\\nl2'"),
				"self.val1 == '''l1\nl2'''",
			},
		},
		{name: "bytes",
			obj:    objs("QUI=", "QUI="),
			schema: schemas(byteType, byteType),
			valid: []string{
				"self.val1 == self.val2",
				"self.val1 == b'AB'",
				"self.val1 == bytes('AB')",
				"self.val1 == b'\\x41\\x42'",
				"type(self.val1) == bytes",
				"size(self.val1) == 2",
			},
		},
		{name: "booleans",
			obj:    objs(true, true, false, false),
			schema: schemas(booleanType, booleanType, booleanType, booleanType),
			valid: []string{
				ValsEqualThemselvesAndDataLiteral("self.val1", "self.val2", "true"),
				ValsEqualThemselvesAndDataLiteral("self.val3", "self.val4", "false"),
				"self.val1 != self.val4",
				"type(self.val1) == bool",
			},
		},
		{name: "duration format",
			obj:    objs("1h2m3s4ms", "1h2m3s4ms"),
			schema: schemas(durationFormat, durationFormat),
			valid: []string{
				ValsEqualThemselvesAndDataLiteral("self.val1", "self.val2", "duration('1h2m3s4ms')"),
				"self.val1 == duration('1h2m') + duration('3s4ms')",
				"self.val1.getHours() == 1",
				"self.val1.getMinutes() == 62",
				"self.val1.getSeconds() == 3723",
				"self.val1.getMilliseconds() == 3723004",
				"type(self.val1) == google.protobuf.Duration",
			},
		},
		{name: "date format",
			obj:    objs("1997-07-16", "1997-07-16"),
			schema: schemas(dateFormat, dateFormat),
			valid: []string{
				ValsEqualThemselvesAndDataLiteral("self.val1", "self.val2", "timestamp('1997-07-16T00:00:00.000Z')"),
				"self.val1.getDate() == 16",
				"self.val1.getMonth() == 06", // zero based indexing
				"self.val1.getFullYear() == 1997",
				"type(self.val1) == google.protobuf.Timestamp",
			},
		},
		{name: "date-time format",
			obj:    objs("2011-08-18T19:03:37.010000000+01:00", "2011-08-18T19:03:37.010000000+01:00"),
			schema: schemas(dateTimeFormat, dateTimeFormat),
			valid: []string{
				ValsEqualThemselvesAndDataLiteral("self.val1", "self.val2", "timestamp('2011-08-18T19:03:37.010+01:00')"),
				"self.val1 == timestamp('2011-08-18T00:00:00.000+01:00') + duration('19h3m37s10ms')",
				"self.val1.getDate('01:00') == 18",
				"self.val1.getMonth('01:00') == 7", // zero based indexing
				"self.val1.getFullYear('01:00') == 2011",
				"self.val1.getHours('01:00') == 19",
				"self.val1.getMinutes('01:00') == 03",
				"self.val1.getSeconds('01:00') == 37",
				"self.val1.getMilliseconds('01:00') == 10",
				"self.val1.getHours('UTC') == 18", // TZ in string is 1hr off of UTC
				"type(self.val1) == google.protobuf.Timestamp",
			},
		},
		{name: "enums",
			obj: map[string]interface{}{"enumStr": "Pending"},
			schema: objectTypePtr(map[string]schema.Structural{"enumStr": {
				Generic: schema.Generic{
					Type: "string",
				},
				ValueValidation: &schema.ValueValidation{
					Enum: []schema.JSON{
						{Object: "Pending"},
						{Object: "Available"},
						{Object: "Bound"},
						{Object: "Released"},
						{Object: "Failed"},
					},
				},
			}}),
			valid: []string{
				"self.enumStr == 'Pending'",
				"self.enumStr in ['Pending', 'Available']",
			},
		},
		{name: "conversions",
			obj:    objs(int64(10), 10.0, 10.49, 10.5, true, "10", "MTA=", "3723.004s", "1h2m3s4ms", "2011-08-18T19:03:37.01+01:00", "2011-08-18T19:03:37.01+01:00", "2011-08-18T00:00:00Z", "2011-08-18"),
			schema: schemas(integerType, numberType, numberType, numberType, booleanType, stringType, byteType, stringType, durationFormat, stringType, dateTimeFormat, stringType, dateFormat),
			valid: []string{
				"int(self.val2) == self.val1",
				"int(self.val3) == self.val1",
				"int(self.val4) == self.val1",
				"int(self.val6) == self.val1",
				"double(self.val1) == self.val2",
				"double(self.val6) == self.val2",
				"bytes(self.val6) == self.val7",
				"string(self.val1) == self.val6",
				"string(self.val2) == '10'",
				"string(self.val3) == '10.49'",
				"string(self.val4) == '10.5'",
				"string(self.val5) == 'true'",
				"string(self.val7) == self.val6",
				"duration(self.val8) == self.val9",
				"string(self.val9) == self.val8",
				"timestamp(self.val10) == self.val11",
				"string(self.val11) == self.val10",
				"timestamp(self.val12) == self.val13",
				"string(self.val13) == self.val12",
			},
		},
		{name: "lists",
			obj:    objs([]interface{}{1, 2, 3}, []interface{}{1, 2, 3}),
			schema: schemas(listType(&integerType), listType(&integerType)),
			valid: []string{
				ValsEqualThemselvesAndDataLiteral("self.val1", "self.val2", "[1, 2, 3]"),
				"1 in self.val1",
				"self.val2[0] in self.val1",
				"!(0 in self.val1)",
				"self.val1 + self.val2 == [1, 2, 3, 1, 2, 3]",
				"self.val1 + [4, 5] == [1, 2, 3, 4, 5]",
			},
		},
		{name: "listSets",
			obj:    objs([]interface{}{"a", "b", "c"}, []interface{}{"a", "c", "b"}),
			schema: schemas(listSetType(&stringType), listSetType(&stringType)),
			valid: []string{
				// equal even though order is different
				"self.val1 == ['c', 'b', 'a']",
				"self.val1 == self.val2",
				"'a' in self.val1",
				"self.val2[0] in self.val1",
				"!('x' in self.val1)",
				"self.val1 + self.val2 == ['a', 'b', 'c']",
				"self.val1 + ['c', 'd'] == ['a', 'b', 'c', 'd']",
			},
		},
		{name: "listMaps",
			obj: map[string]interface{}{
				"objs": []interface{}{
					[]interface{}{
						map[string]interface{}{"k": "a", "v": "1"},
						map[string]interface{}{"k": "b", "v": "2"},
					},
					[]interface{}{
						map[string]interface{}{"k": "b", "v": "2"},
						map[string]interface{}{"k": "a", "v": "1"},
					},
					[]interface{}{
						map[string]interface{}{"k": "b", "v": "3"},
						map[string]interface{}{"k": "a", "v": "1"},
					},
					[]interface{}{
						map[string]interface{}{"k": "c", "v": "4"},
					},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"objs": listType(listMapTypePtr([]string{"k"}, objectTypePtr(map[string]schema.Structural{
					"k": stringType,
					"v": stringType,
				}))),
			}),
			valid: []string{
				"self.objs[0] == self.objs[1]",                // equal even though order is different
				"self.objs[0] + self.objs[2] == self.objs[2]", // rhs overwrites lhs values
				"self.objs[2] + self.objs[0] == self.objs[0]",

				"self.objs[0] == [self.objs[0][0], self.objs[0][1]]", // equal against a declared list
				"self.objs[0] == [self.objs[0][1], self.objs[0][0]]",

				"self.objs[2] + [self.objs[0][0], self.objs[0][1]] == self.objs[0]", // concat against a declared list
				"size(self.objs[0] + [self.objs[3][0]]) == 3",
			},
			errors: map[string]string{
				"self.objs[0] == {'k': 'a', 'v': '1'}": "no matching overload for '_==_'", // objects cannot be compared against a data literal map
			},
		},
		{name: "maps",
			obj:    objs(map[string]interface{}{"k1": "a", "k2": "b"}, map[string]interface{}{"k2": "b", "k1": "a"}),
			schema: schemas(mapType(&stringType), mapType(&stringType)),
			valid: []string{
				"self.val1 == self.val2", // equal even though order is different
				"'k1' in self.val1",
				"!('k3' in self.val1)",
				"self.val1 == {'k1': 'a', 'k2': 'b'}",
			},
		},
		{name: "objects",
			obj: map[string]interface{}{
				"objs": []interface{}{
					map[string]interface{}{"f1": "a", "f2": "b"},
					map[string]interface{}{"f1": "a", "f2": "b"},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"objs": listType(objectTypePtr(map[string]schema.Structural{
					"f1": stringType,
					"f2": stringType,
				})),
			}),
			valid: []string{
				"self.objs[0] == self.objs[1]",
			},
			errors: map[string]string{
				"self.objs[0] == {'f1': 'a', 'f2': 'b'}": "found no matching overload for '_==_'", // objects cannot be compared against a data literal map
			},
		},
		{name: "object access",
			obj: map[string]interface{}{
				"a": map[string]interface{}{
					"b": 1,
					"d": nil,
				},
				"a1": map[string]interface{}{
					"b1": map[string]interface{}{
						"c1": 4,
					},
				},
				"a3": map[string]interface{}{},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"a": objectType(map[string]schema.Structural{
					"b": integerType,
					"c": integerType,
					"d": withNullable(true, integerType),
				}),
				"a1": objectType(map[string]schema.Structural{
					"b1": objectType(map[string]schema.Structural{
						"c1": integerType,
					}),
					"d2": objectType(map[string]schema.Structural{
						"e2": integerType,
					}),
				}),
			}),
			// https://github.com/google/cel-spec/blob/master/doc/langdef.md#field-selection
			valid: []string{
				"has(self.a.b)",
				"!has(self.a.c)",
				"!has(self.a.d)", // We treat null value fields the same as absent fields in CEL
				"has(self.a1.b1.c1)",
				"!(has(self.a1.d2) && has(self.a1.d2.e2))", // must check intermediate optional fields (see below no such key error for d2)
				"!has(self.a1.d2)",
			},
			errors: map[string]string{
				"has(self.a.z)":      "undefined field 'z'",             // may not reference undefined fields, not even with has
				"self.a['b'] == 1":   "no matching overload for '_[_]'", // only allowed on maps, not objects
				"has(self.a1.d2.e2)": "no such key: d2",                 // has only checks last element in path, when d2 is absent in value, this is an error
			},
		},
		{name: "map access",
			obj: map[string]interface{}{
				"val": map[string]interface{}{
					"b": 1,
					"d": 2,
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"val": mapType(&integerType),
			}),
			valid: []string{
				// idiomatic map access
				"!('a' in self.val)",
				"'b' in self.val",
				"!('c' in self.val)",
				"'d' in self.val",
				// field selection also possible if map key is a valid CEL identifier
				"!has(self.val.a)",
				"has(self.val.b)",
				"!has(self.val.c)",
				"has(self.val.d)",
				"self.val.all(k, self.val[k] > 0)",
				"self.val.exists(k, self.val[k] > 1)",
				"self.val.exists_one(k, self.val[k] == 2)",
				"!self.val.exists(k, self.val[k] > 2)",
				"!self.val.exists_one(k, self.val[k] > 0)",
				"size(self.val) == 2",
				"self.val.map(k, self.val[k]).exists(v, v == 1)",
				"size(self.val.filter(k, self.val[k] > 1)) == 1",
			},
			errors: map[string]string{
				"self.val['c'] == 1": "no such key: c",
			},
		},
		{name: "listMap access",
			obj: map[string]interface{}{
				"listMap": []interface{}{
					map[string]interface{}{"k": "a1", "v": "b1"},
					map[string]interface{}{"k": "a2", "v": "b2"},
					map[string]interface{}{"k": "a3", "v": "b3", "v2": "z"},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"listMap": listMapType([]string{"k"}, objectTypePtr(map[string]schema.Structural{
					"k":  stringType,
					"v":  stringType,
					"v2": stringType,
				})),
			}),
			valid: []string{
				"has(self.listMap[0].v)",
				"self.listMap.all(m, m.k.startsWith('a'))",
				"self.listMap.all(m, !has(m.v2) || m.v2 == 'z')",
				"self.listMap.exists(m, m.k.endsWith('1'))",
				"self.listMap.exists_one(m, m.k == 'a3')",
				"!self.listMap.all(m, m.k.endsWith('1'))",
				"!self.listMap.exists(m, m.v == 'x')",
				"!self.listMap.exists_one(m, m.k.startsWith('a'))",
				"size(self.listMap.filter(m, m.k == 'a1')) == 1",
				"self.listMap.exists(m, m.k == 'a1' && m.v == 'b1')",
				"self.listMap.map(m, m.v).exists(v, v == 'b1')",

				// test comprehensions where the field used in predicates is unset on all but one of the elements:
				// - with has checks:

				"self.listMap.exists(m, has(m.v2) && m.v2 == 'z')",
				"!self.listMap.all(m, has(m.v2) && m.v2 != 'z')",
				"self.listMap.exists_one(m, has(m.v2) && m.v2 == 'z')",
				"self.listMap.filter(m, has(m.v2) && m.v2 == 'z').size() == 1",
				// undocumented overload of map that takes a filter argument. This is the same as .filter().map()
				"self.listMap.map(m, has(m.v2) && m.v2 == 'z', m.v2).size() == 1",
				"self.listMap.filter(m, has(m.v2) && m.v2 == 'z').map(m, m.v2).size() == 1",
				// - without has checks:

				// all() and exists() macros ignore errors from predicates so long as the condition holds for at least one element
				"self.listMap.exists(m, m.v2 == 'z')",
				"!self.listMap.all(m, m.v2 != 'z')",
			},
			errors: map[string]string{
				// test comprehensions where the field used in predicates is unset on all but one of the elements: (error cases)
				// - without has checks:

				// if all() predicate evaluates to false or error for all elements, any error encountered is raised
				"self.listMap.all(m, m.v2 == 'z')": "no such key: v2",
				// exists one() is stricter than map() or exists(), it requires exactly one predicate evaluate to true and the rest
				// evaluate to false, any error encountered is raised
				"self.listMap.exists_one(m, m.v2 == 'z')": "no such key: v2",
				// filter and map raise any error encountered
				"self.listMap.filter(m, m.v2 == 'z').size() == 1": "no such key: v2",
				"self.listMap.map(m, m.v2).size() == 1":           "no such key: v2",
				// undocumented overload of map that takes a filter argument. This is the same as .filter().map()
				"self.listMap.map(m, m.v2 == 'z', m.v2).size() == 1": "no such key: v2",
			},
		},
		{name: "list access",
			obj: map[string]interface{}{
				"array": []interface{}{1, 1, 2, 2, 3, 3, 4, 5},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"array": listType(&integerType),
			}),
			valid: []string{
				"2 in self.array",
				"self.array.all(e, e > 0)",
				"self.array.exists(e, e > 2)",
				"self.array.exists_one(e, e > 4)",
				"!self.array.all(e, e < 2)",
				"!self.array.exists(e, e < 0)",
				"!self.array.exists_one(e, e == 2)",
				"self.array.all(e, e < 100)",
				"size(self.array.filter(e, e%2 == 0)) == 3",
				"self.array.map(e, e * 20).filter(e, e > 50).exists(e, e == 60)",
				"size(self.array) == 8",
			},
			errors: map[string]string{
				"self.array[100] == 0": "index out of bounds: 100",
			},
		},
		{name: "listSet access",
			obj: map[string]interface{}{
				"set": []interface{}{1, 2, 3, 4, 5},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"set": listType(&integerType),
			}),
			valid: []string{
				"3 in self.set",
				"self.set.all(e, e > 0)",
				"self.set.exists(e, e > 3)",
				"self.set.exists_one(e, e == 3)",
				"!self.set.all(e, e < 3)",
				"!self.set.exists(e, e < 0)",
				"!self.set.exists_one(e, e > 3)",
				"self.set.all(e, e < 10)",
				"size(self.set.filter(e, e%2 == 0)) == 2",
				"self.set.map(e, e * 20).filter(e, e > 50).exists_one(e, e == 60)",
				"size(self.set) == 5",
			},
		},
		{name: "typemeta and objectmeta access specified",
			obj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata": map[string]interface{}{
					"name":         "foo",
					"generateName": "pickItForMe",
					"namespace":    "xyz",
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"kind":       stringType,
				"apiVersion": stringType,
				"metadata": objectType(map[string]schema.Structural{
					"name":         stringType,
					"generateName": stringType,
				}),
			}),
			valid: []string{
				"self.kind == 'Pod'",
				"self.apiVersion == 'v1'",
				"self.metadata.name == 'foo'",
				"self.metadata.generateName == 'pickItForMe'",
			},
			errors: map[string]string{
				"has(self.metadata.namespace)": "undefined field 'namespace'",
			},
		},
		{name: "typemeta and objectmeta access not specified",
			isRoot: true,
			obj: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata": map[string]interface{}{
					"name":         "foo",
					"generateName": "pickItForMe",
					"namespace":    "xyz",
				},
				"spec": map[string]interface{}{
					"field1": "a",
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"spec": objectType(map[string]schema.Structural{
					"field1": stringType,
				}),
			}),
			valid: []string{
				"self.kind == 'Pod'",
				"self.apiVersion == 'v1'",
				"self.metadata.name == 'foo'",
				"self.metadata.generateName == 'pickItForMe'",
				"self.spec.field1 == 'a'",
			},
			errors: map[string]string{
				"has(self.metadata.namespace)": "undefined field 'namespace'",
			},
		},

		// Kubernetes special types
		{name: "embedded object",
			obj: map[string]interface{}{
				"embedded": map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata": map[string]interface{}{
						"name":         "foo",
						"generateName": "pickItForMe",
						"namespace":    "xyz",
					},
					"spec": map[string]interface{}{
						"field1": "a",
					},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"embedded": {
					Generic: schema.Generic{Type: "object"},
					Extensions: schema.Extensions{
						XEmbeddedResource: true,
					},
				},
			}),
			valid: []string{
				// 'kind', 'apiVersion', 'metadata.name' and 'metadata.generateName' are always accessible
				// even if not specified in the schema.
				"self.embedded.kind == 'Pod'",
				"self.embedded.apiVersion == 'v1'",
				"self.embedded.metadata.name == 'foo'",
				"self.embedded.metadata.generateName == 'pickItForMe'",
			},
			// only field declared in the schema can be field selected in CEL expressions
			errors: map[string]string{
				"has(self.embedded.metadata.namespace)": "undefined field 'namespace'",
				"has(self.embedded.spec)":               "undefined field 'spec'",
			},
		},
		{name: "embedded object with properties",
			obj: map[string]interface{}{
				"embedded": map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata": map[string]interface{}{
						"name":         "foo",
						"generateName": "pickItForMe",
						"namespace":    "xyz",
					},
					"spec": map[string]interface{}{
						"field1": "a",
					},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"embedded": {
					Generic: schema.Generic{Type: "object"},
					Extensions: schema.Extensions{
						XEmbeddedResource: true,
					},
					Properties: map[string]schema.Structural{
						"kind":       stringType,
						"apiVersion": stringType,
						"metadata": objectType(map[string]schema.Structural{
							"name":         stringType,
							"generateName": stringType,
						}),
						"spec": objectType(map[string]schema.Structural{
							"field1": stringType,
						}),
					},
				},
			}),
			valid: []string{
				// in this case 'kind', 'apiVersion', 'metadata.name' and 'metadata.generateName' are specified in the
				// schema, but they would be accessible even if they were not
				"self.embedded.kind == 'Pod'",
				"self.embedded.apiVersion == 'v1'",
				"self.embedded.metadata.name == 'foo'",
				"self.embedded.metadata.generateName == 'pickItForMe'",
				// the specified embedded fields are accessible
				"self.embedded.spec.field1 == 'a'",
			},
			errors: map[string]string{
				// only name and generateName are accessible on metadata
				"has(self.embedded.metadata.namespace)": "undefined field 'namespace'",
			},
		},
		{name: "embedded object with preserve unknown",
			obj: map[string]interface{}{
				"embedded": map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Pod",
					"metadata": map[string]interface{}{
						"name":         "foo",
						"generateName": "pickItForMe",
						"namespace":    "xyz",
					},
					"spec": map[string]interface{}{
						"field1": "a",
					},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"embedded": {
					Generic: schema.Generic{Type: "object"},
					Extensions: schema.Extensions{
						XPreserveUnknownFields: true,
						XEmbeddedResource:      true,
					},
				},
			}),
			valid: []string{
				// 'kind', 'apiVersion', 'metadata.name' and 'metadata.generateName' are always accessible
				// even if not specified in the schema, regardless of if x-kubernetes-preserve-unknown-fields is set.
				"self.embedded.kind == 'Pod'",
				"self.embedded.apiVersion == 'v1'",
				"self.embedded.metadata.name == 'foo'",
				"self.embedded.metadata.generateName == 'pickItForMe'",

				// the object exists
				"has(self.embedded)",
			},
			// only field declared in the schema can be field selected in CEL expressions, regardless of if
			// x-kubernetes-preserve-unknown-fields is set.
			errors: map[string]string{
				"has(self.embedded.spec)": "undefined field 'spec'",
			},
		},
		{name: "string in intOrString",
			obj: map[string]interface{}{
				"something": "25%",
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"something": intOrStringType(),
			}),
			valid: []string{
				// In Kubernetes 1.24 and later, the CEL type returns false for an int-or-string comparison against the
				// other type, making it safe to write validation rules like:
				"self.something == '25%'",
				"self.something != 1",
				"self.something == 1 || self.something == '25%'",
				"self.something == '25%' || self.something == 1",

				// In Kubernetes 1.23 and earlier, all int-or-string access must be guarded by a type check to prevent
				// a runtime error attempting an equality check between string and int types.
				"type(self.something) == string && self.something == '25%'",
				"type(self.something) == int ? self.something == 1 : self.something == '25%'",

				// Because the type is dynamic it receives no type checking, and evaluates to false when compared to
				// other types at runtime.
				"self.something != ['anything']",
			},
		},
		{name: "int in intOrString",
			obj: map[string]interface{}{
				"something": int64(1),
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"something": intOrStringType(),
			}),
			valid: []string{
				// In Kubernetes 1.24 and later, the CEL type returns false for an int-or-string comparison against the
				// other type, making it safe to write validation rules like:
				"self.something == 1",
				"self.something != 'some string'",
				"self.something == 1 || self.something == '25%'",
				"self.something == '25%' || self.something == 1",

				// In Kubernetes 1.23 and earlier, all int-or-string access must be guarded by a type check to prevent
				// a runtime error attempting an equality check between string and int types.
				"type(self.something) == int && self.something == 1",
				"type(self.something) == int ? self.something == 1 : self.something == '25%'",

				// Because the type is dynamic it receives no type checking, and evaluates to false when compared to
				// other types at runtime.
				"self.something != ['anything']",
			},
		},
		{name: "null in intOrString",
			obj: map[string]interface{}{
				"something": nil,
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"something": withNullable(true, intOrStringType()),
			}),
			valid: []string{
				"!has(self.something)",
			},
			errors: map[string]string{
				"type(self.something) == int": "no such key",
			},
		},
		{name: "percent comparison using intOrString",
			obj: map[string]interface{}{
				"min":       "50%",
				"current":   5,
				"available": 10,
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"min":       intOrStringType(),
				"current":   integerType,
				"available": integerType,
			}),
			valid: []string{
				// validate that if 'min' is a string that it is a percentage
				`type(self.min) == string && self.min.matches(r'(\d+(\.\d+)?%)')`,
				// validate that 'min' can be either a exact value minimum, or a minimum as a percentage of 'available'
				"type(self.min) == int ? self.current <= self.min : double(self.current) / double(self.available) >= double(self.min.replace('%', '')) / 100.0",
			},
		},
		{name: "preserve unknown fields",
			obj: map[string]interface{}{
				"withUnknown": map[string]interface{}{
					"field1": "a",
					"field2": "b",
				},
				"withUnknownList": []interface{}{
					map[string]interface{}{
						"field1": "a",
						"field2": "b",
					},
					map[string]interface{}{
						"field1": "x",
						"field2": "y",
					},
					map[string]interface{}{
						"field1": "x",
						"field2": "y",
					},
					map[string]interface{}{},
					map[string]interface{}{},
				},
				"withUnknownFieldList": []interface{}{
					map[string]interface{}{
						"fieldOfUnknownType": "a",
					},
					map[string]interface{}{
						"fieldOfUnknownType": 1,
					},
					map[string]interface{}{
						"fieldOfUnknownType": 1,
					},
				},
				"anyvalList":   []interface{}{"a", 2},
				"anyvalMap":    map[string]interface{}{"k": "1"},
				"anyvalField1": 1,
				"anyvalField2": "a",
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"withUnknown": {
					Generic: schema.Generic{Type: "object"},
					Extensions: schema.Extensions{
						XPreserveUnknownFields: true,
					},
				},
				"withUnknownList": listType(&schema.Structural{
					Generic: schema.Generic{Type: "object"},
					Extensions: schema.Extensions{
						XPreserveUnknownFields: true,
					},
				}),
				"withUnknownFieldList": listType(&schema.Structural{
					Generic: schema.Generic{Type: "object"},
					Properties: map[string]schema.Structural{
						"fieldOfUnknownType": {
							Extensions: schema.Extensions{
								XPreserveUnknownFields: true,
							},
						},
					},
				}),
				"anyvalList": listType(&schema.Structural{
					Extensions: schema.Extensions{
						XPreserveUnknownFields: true,
					},
				}),
				"anyvalMap": mapType(&schema.Structural{
					Extensions: schema.Extensions{
						XPreserveUnknownFields: true,
					},
				}),
				"anyvalField1": {
					Extensions: schema.Extensions{
						XPreserveUnknownFields: true,
					},
				},
				"anyvalField2": {
					Extensions: schema.Extensions{
						XPreserveUnknownFields: true,
					},
				},
			}),
			valid: []string{
				"has(self.withUnknown)",
				"self.withUnknownList.size() == 5",
				// fields that are unknown because they were not specified on the object schema are included in equality checks
				"self.withUnknownList[0] != self.withUnknownList[1]",
				"self.withUnknownList[1] == self.withUnknownList[2]",
				"self.withUnknownList[3] == self.withUnknownList[4]",

				// fields specified on the object schema that are unknown because the field's schema is unknown are also included equality checks
				"self.withUnknownFieldList[0] != self.withUnknownFieldList[1]",
				"self.withUnknownFieldList[1] == self.withUnknownFieldList[2]",
			},
			errors: map[string]string{
				// unknown fields cannot be field selected
				"has(self.withUnknown.field1)": "undefined field 'field1'",
				// if items type of a list, or additionalProperties type of a map, is unknown we treat entire list or map as unknown
				// If the type of a property is unknown it is not accessible in CEL
				"has(self.anyvalList)":   "undefined field 'anyvalList'",
				"has(self.anyvalMap)":    "undefined field 'anyvalMap'",
				"has(self.anyvalField1)": "undefined field 'anyvalField1'",
				"has(self.anyvalField2)": "undefined field 'anyvalField2'",
			},
		},
		{name: "known and unknown fields",
			obj: map[string]interface{}{
				"withUnknown": map[string]interface{}{
					"known":   1,
					"unknown": "a",
				},
				"withUnknownList": []interface{}{
					map[string]interface{}{
						"known":   1,
						"unknown": "a",
					},
					map[string]interface{}{
						"known":   1,
						"unknown": "b",
					},
					map[string]interface{}{
						"known":   1,
						"unknown": "b",
					},
					map[string]interface{}{
						"known": 1,
					},
					map[string]interface{}{
						"known": 1,
					},
					map[string]interface{}{
						"known": 2,
					},
				},
			},
			schema: &schema.Structural{
				Generic: schema.Generic{
					Type: "object",
				},
				Properties: map[string]schema.Structural{
					"withUnknown": {
						Generic: schema.Generic{Type: "object"},
						Extensions: schema.Extensions{
							XPreserveUnknownFields: true,
						},
						Properties: map[string]schema.Structural{
							"known": integerType,
						},
					},
					"withUnknownList": listType(&schema.Structural{
						Generic: schema.Generic{Type: "object"},
						Extensions: schema.Extensions{
							XPreserveUnknownFields: true,
						},
						Properties: map[string]schema.Structural{
							"known": integerType,
						},
					}),
				},
			},
			valid: []string{
				"self.withUnknown.known == 1",
				"self.withUnknownList[0] != self.withUnknownList[1]",
				// if the unknown fields are the same, they are equal
				"self.withUnknownList[1] == self.withUnknownList[2]",

				// if unknown fields are different, they are not equal
				"self.withUnknownList[0] != self.withUnknownList[1]",
				"self.withUnknownList[0] != self.withUnknownList[3]",
				"self.withUnknownList[0] != self.withUnknownList[5]",

				// if all fields are known, equality works as usual
				"self.withUnknownList[3] == self.withUnknownList[4]",
				"self.withUnknownList[4] != self.withUnknownList[5]",
			},
			// only field declared in the schema can be field selected in CEL expressions
			errors: map[string]string{
				"has(self.withUnknown.unknown)": "undefined field 'unknown'",
			},
		},
		{name: "field nullability",
			obj: map[string]interface{}{
				"setPlainStr":          "v1",
				"setDefaultedStr":      "v2",
				"setNullableStr":       "v3",
				"setToNullNullableStr": nil,

				// we don't run the defaulter in this test suite (depending on it would introduce a cycle)
				// so we fake it :(
				"unsetDefaultedStr": "default value",
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"unsetPlainStr":     stringType,
				"unsetDefaultedStr": withDefault("default value", stringType),
				"unsetNullableStr":  withNullable(true, stringType),

				"setPlainStr":          stringType,
				"setDefaultedStr":      withDefault("default value", stringType),
				"setNullableStr":       withNullable(true, stringType),
				"setToNullNullableStr": withNullable(true, stringType),
			}),
			valid: []string{
				"!has(self.unsetPlainStr)",
				"has(self.unsetDefaultedStr) && self.unsetDefaultedStr == 'default value'",
				"!has(self.unsetNullableStr)",

				"has(self.setPlainStr) && self.setPlainStr == 'v1'",
				"has(self.setDefaultedStr) && self.setDefaultedStr == 'v2'",
				"has(self.setNullableStr) && self.setNullableStr == 'v3'",
				// We treat null fields as absent fields, not as null valued fields.
				// Note that this is different than how we treat nullable list items or map values.
				"type(self.setNullableStr) != null_type",

				// a field that is set to null is treated the same as an absent field in validation rules
				"!has(self.setToNullNullableStr)",
			},
			errors: map[string]string{
				// the types used in validation rules don't integrate with CEL's Null type, so
				// all attempts to compare values with null are caught by the type checker at compilation time
				"self.unsetPlainStr == null":     "no matching overload for '_==_' applied to '(string, null)",
				"self.unsetDefaultedStr != null": "no matching overload for '_!=_' applied to '(string, null)",
				"self.unsetNullableStr == null":  "no matching overload for '_==_' applied to '(string, null)",
				"self.setPlainStr != null":       "no matching overload for '_!=_' applied to '(string, null)",
				"self.setDefaultedStr != null":   "no matching overload for '_!=_' applied to '(string, null)",
				"self.setNullableStr != null":    "no matching overload for '_!=_' applied to '(string, null)",
			},
		},
		{name: "null values in container types",
			obj: map[string]interface{}{
				"m": map[string]interface{}{
					"a": nil,
					"b": "not-nil",
				},
				"l": []interface{}{
					nil, "not-nil",
				},
				"s": []interface{}{
					nil, "not-nil",
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"m": mapType(withNullablePtr(true, stringType)),
				"l": listType(withNullablePtr(true, stringType)),
				"s": listSetType(withNullablePtr(true, stringType)),
			}),
			valid: []string{
				"self.m.size() == 2",
				"'a' in self.m",
				"type(self.m['a']) == null_type", // null check using runtime type checking
				//"self.m['a'] == null",

				"self.l.size() == 2",
				"type(self.l[0]) == null_type",
				//"self.l[0] == null",

				"self.s.size() == 2",
				"type(self.s[0]) == null_type",
				//"self.s[0] == null",
			},
			errors: map[string]string{
				// TODO(jpbetz): Type checker does not support unions of (<type>, Null).
				// This will be available when "heterogeneous type" supported is added to cel-go.
				// In the meantime, the only other option would be to use dynamic types for nullable types, which
				// would disable type checking. We plan to wait for "heterogeneous type" support.
				//"self.m['a'] == null": "found no matching overload for '_==_' applied to '(string, null)",
				//"self.l[0] == null": "found no matching overload for '_==_' applied to '(string, null)",
				//"self.s[0] == null": "found no matching overload for '_==_' applied to '(string, null)",
			},
		},
		{name: "escaping",
			obj: map[string]interface{}{
				// RESERVED symbols defined in the CEL lexer
				"true": 1, "false": 2, "null": 3, "in": 4, "as": 5,
				"break": 6, "const": 7, "continue": 8, "else": 9,
				"for": 10, "function": 11, "if": 12, "import": 13,
				"let": 14, "loop": 15, "package": 16, "namespace": 17,
				"return": 18, "var": 19, "void": 20, "while": 21,
				// identifiers that are part of the CEL language
				"int": 101, "uint": 102, "double": 103, "bool": 104,
				"string": 105, "bytes": 106, "list": 107, "map": 108,
				"null_type": 109, "type": 110,
				// validation expression reserved identifiers
				"self": 201,
				// identifiers of CEL builtin function and macro names
				"getDate": 202,
				"all":     203,
				"size":    "204",
				// identifiers that have _s
				"_true": 301,
				// identifiers that have the characters we escape
				"dot.dot":                            401,
				"dash-dash":                          402,
				"slash/slash":                        403,
				"underscore_underscore":              404, // ok, this is not so weird, but it's one we'd like a test case for
				"doubleunderscore__doubleunderscore": 405,
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"true": integerType, "false": integerType, "null": integerType, "in": integerType, "as": integerType,
				"break": integerType, "const": integerType, "continue": integerType, "else": integerType,
				"for": integerType, "function": integerType, "if": integerType, "import": integerType,
				"let": integerType, "loop": integerType, "package": integerType, "namespace": integerType,
				"return": integerType, "var": integerType, "void": integerType, "while": integerType,

				"int": integerType, "uint": integerType, "double": integerType, "bool": integerType,
				"string": integerType, "bytes": integerType, "list": integerType, "map": integerType,
				"null_type": integerType, "type": integerType,

				"self": integerType,

				"getDate": integerType,
				"all":     integerType,
				"size":    stringType,

				"_true": integerType,

				"dot.dot":                            integerType,
				"dash-dash":                          integerType,
				"slash/slash":                        integerType,
				"underscore_underscore":              integerType, // ok, this is not so weird, but it's one we'd like a test case for
				"doubleunderscore__doubleunderscore": integerType,
			}),
			valid: []string{
				// CEL lexer RESERVED keywords must be escaped
				"self.__true__ == 1", "self.__false__ == 2", "self.__null__ == 3", "self.__in__ == 4", "self.__as__ == 5",
				"self.__break__ == 6", "self.__const__ == 7", "self.__continue__ == 8", "self.__else__ == 9",
				"self.__for__ == 10", "self.__function__ == 11", "self.__if__ == 12", "self.__import__ == 13",
				"self.__let__ == 14", "self.__loop__ == 15", "self.__package__ == 16", "self.__namespace__ == 17",
				"self.__return__ == 18", "self.__var__ == 19", "self.__void__ == 20", "self.__while__ == 21", "self.__true__ == 1",

				// CEL language identifiers do not need to be escaped, but would collide with builtin language identifier if bound as
				// root variable names, so are only field selectable as a field of 'self'.
				"self.int == 101", "self.uint == 102", "self.double == 103", "self.bool == 104",
				"self.string == 105", "self.bytes == 106", "self.list == 107", "self.map == 108",
				"self.null_type == 109", "self.type == 110",

				// if a property name is 'self', it can be field selected as 'self.self' (but not as just 'self' because we bind that
				// variable name to the locally scoped expression value.
				"self.self == 201",
				// CEL macro and function names do not need to be escaped because the parser can disambiguate them from the function and
				// macro identifiers.
				"self.getDate == 202",
				"self.all == 203",
				"self.size == '204'",
				// _ is not escaped
				"self._true == 301",
				"self.__true__ != 301",

				"self.dot__dot__dot == 401",
				"self.dash__dash__dash == 402",
				"self.slash__slash__slash == 403",
				"self.underscore_underscore == 404",
				"self.doubleunderscore__underscores__doubleunderscore == 405",
			},
			errors: map[string]string{
				// 'true' is a boolean literal, not a field name
				"self.true == 1": "mismatched input 'true' expecting IDENTIFIER",
				// 'self' is the locally scoped expression value
				"self == 201": "found no matching overload for '_==_'",
				// attempts to use identifiers that are not escapable are caught by the compiler since
				// we don't register declarations for them
				"self.__illegal__ == 301": "undefined field '__illegal__'",
			},
		},
		{name: "map keys are not escaped",
			obj: map[string]interface{}{
				"m": map[string]interface{}{
					"@":   1,
					"9":   2,
					"int": 3,
					"./-": 4,
					"👑":   5,
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{"m": mapType(&integerType)}),
			valid: []string{
				"self.m['@'] == 1",
				"self.m['9'] == 2",
				"self.m['int'] == 3",
				"self.m['./-'] == 4",
				"self.m['👑'] == 5",
			},
		},
		{name: "object types are not accessible",
			obj: map[string]interface{}{
				"nestedInMap": map[string]interface{}{
					"k1": map[string]interface{}{
						"inMapField": 1,
					},
					"k2": map[string]interface{}{
						"inMapField": 2,
					},
				},
				"nestedInList": []interface{}{
					map[string]interface{}{
						"inListField": 1,
					},
					map[string]interface{}{
						"inListField": 2,
					},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"nestedInMap": mapType(objectTypePtr(map[string]schema.Structural{
					"inMapField": integerType,
				})),
				"nestedInList": listType(objectTypePtr(map[string]schema.Structural{
					"inListField": integerType,
				})),
			}),
			valid: []string{
				// we do not expose a stable type for the self variable, even when it is an object that CEL
				// considers a named type. The only operation developers should be able to perform on the type is
				// equality checking.
				"type(self) == type(self)",
				"type(self.nestedInMap['k1']) == type(self.nestedInMap['k2'])",
				"type(self.nestedInList[0]) == type(self.nestedInList[1])",
			},
			errors: map[string]string{
				// Note that errors, like the below, do print the type name, but it changes each time a CRD is updated.
				// Type name printed in the below error will be of the form "<uuid>.nestedInList.@idx".

				// Developers may not cast the type of variables as a string:
				"string(type(self.nestedInList[0])).endsWith('.nestedInList.@idx')": "found no matching overload for 'string' applied to '(type",
			},
		},
		{name: "listMaps with unsupported identity characters in property names",
			obj: map[string]interface{}{
				"objs": []interface{}{
					[]interface{}{
						map[string]interface{}{"k!": "a", "k.": "1"},
						map[string]interface{}{"k!": "b", "k.": "2"},
					},
					[]interface{}{
						map[string]interface{}{"k!": "b", "k.": "2"},
						map[string]interface{}{"k!": "a", "k.": "1"},
					},
					[]interface{}{
						map[string]interface{}{"k!": "b", "k.": "2"},
						map[string]interface{}{"k!": "c", "k.": "1"},
					},
					[]interface{}{
						map[string]interface{}{"k!": "b", "k.": "2"},
						map[string]interface{}{"k!": "a", "k.": "3"},
					},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"objs": listType(listMapTypePtr([]string{"k!", "k."}, objectTypePtr(map[string]schema.Structural{
					"k!": stringType,
					"k.": stringType,
				}))),
			}),
			valid: []string{
				"self.objs[0] == self.objs[1]",    // equal even though order is different
				"self.objs[0][0].k__dot__ == '1'", // '.' is a supported character in identifiers, but it is escaped
				"self.objs[0] != self.objs[2]",    // not equal even though difference is in unsupported char
				"self.objs[0] != self.objs[3]",    // not equal even though difference is in escaped char
			},
			errors: map[string]string{
				// '!' is not a supported character in identifiers, there is no way to access the field
				"self.objs[0][0].k! == '1'": "Syntax error: mismatched input '!' expecting",
			},
		},
		{name: "container type composition",
			obj: map[string]interface{}{
				"obj": map[string]interface{}{
					"field": "a",
				},
				"mapOfMap": map[string]interface{}{
					"x": map[string]interface{}{
						"y": "b",
					},
				},
				"mapOfObj": map[string]interface{}{
					"k": map[string]interface{}{
						"field2": "c",
					},
				},
				"mapOfListMap": map[string]interface{}{
					"o": []interface{}{
						map[string]interface{}{
							"k": "1",
							"v": "d",
						},
					},
				},
				"mapOfList": map[string]interface{}{
					"l": []interface{}{"e"},
				},
				"listMapOfObj": []interface{}{
					map[string]interface{}{
						"k2": "2",
						"v2": "f",
					},
				},
				"listOfMap": []interface{}{
					map[string]interface{}{
						"z": "g",
					},
				},
				"listOfObj": []interface{}{
					map[string]interface{}{
						"field3": "h",
					},
				},
				"listOfListMap": []interface{}{
					[]interface{}{
						map[string]interface{}{
							"k3": "3",
							"v3": "i",
						},
					},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"obj": objectType(map[string]schema.Structural{
					"field": stringType,
				}),
				"mapOfMap": mapType(mapTypePtr(&stringType)),
				"mapOfObj": mapType(objectTypePtr(map[string]schema.Structural{
					"field2": stringType,
				})),
				"mapOfListMap": mapType(listMapTypePtr([]string{"k"}, objectTypePtr(map[string]schema.Structural{
					"k": stringType,
					"v": stringType,
				}))),
				"mapOfList": mapType(listTypePtr(&stringType)),
				"listMapOfObj": listMapType([]string{"k2"}, objectTypePtr(map[string]schema.Structural{
					"k2": stringType,
					"v2": stringType,
				})),
				"listOfMap": listType(mapTypePtr(&stringType)),
				"listOfObj": listType(objectTypePtr(map[string]schema.Structural{
					"field3": stringType,
				})),
				"listOfListMap": listType(listMapTypePtr([]string{"k3"}, objectTypePtr(map[string]schema.Structural{
					"k3": stringType,
					"v3": stringType,
				}))),
			}),
			valid: []string{
				"self.obj.field == 'a'",
				"self.mapOfMap['x']['y'] == 'b'",
				"self.mapOfObj['k'].field2 == 'c'",
				"self.mapOfListMap['o'].exists(e, e.k == '1' && e.v == 'd')",
				"self.mapOfList['l'][0] == 'e'",
				"self.listMapOfObj.exists(e, e.k2 == '2' && e.v2 == 'f')",
				"self.listOfMap[0]['z'] == 'g'",
				"self.listOfObj[0].field3 == 'h'",
				"self.listOfListMap[0].exists(e, e.k3 == '3' && e.v3 == 'i')",
			},
			errors: map[string]string{},
		},
		{name: "invalid data",
			obj: map[string]interface{}{
				"o":           []interface{}{},
				"m":           []interface{}{},
				"l":           map[string]interface{}{},
				"s":           map[string]interface{}{},
				"lm":          map[string]interface{}{},
				"intorstring": true,
				"nullable":    []interface{}{nil},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"o": objectType(map[string]schema.Structural{
					"field": stringType,
				}),
				"m": mapType(&stringType),
				"l": listType(&stringType),
				"s": listSetType(&stringType),
				"lm": listMapType([]string{"k"}, objectTypePtr(map[string]schema.Structural{
					"k": stringType,
					"v": stringType,
				})),
				"intorstring": intOrStringType(),
				"nullable":    listType(&stringType),
			}),
			errors: map[string]string{
				// data is validated before CEL evaluation, so these errors should not be surfaced to end users
				"has(self.o)":             "invalid data, expected a map for the provided schema with type=object",
				"has(self.m)":             "invalid data, expected a map for the provided schema with type=object",
				"has(self.l)":             "invalid data, expected an array for the provided schema with type=array",
				"has(self.s)":             "invalid data, expected an array for the provided schema with type=array",
				"has(self.lm)":            "invalid data, expected an array for the provided schema with type=array",
				"has(self.intorstring)":   "invalid data, expected XIntOrString value to be either a string or integer",
				"self.nullable[0] == 'x'": "invalid data, got null for schema with nullable=false",
				// TODO: also find a way to test the errors returned for: array with no items, object with no properties or additionalProperties, invalid listType and invalid type.
			},
		},
		{name: "stdlib list functions",
			obj: map[string]interface{}{
				"ints":         []interface{}{int64(1), int64(2), int64(2), int64(3)},
				"unsortedInts": []interface{}{int64(2), int64(1)},
				"emptyInts":    []interface{}{},

				"doubles":         []interface{}{float64(1), float64(2), float64(2), float64(3)},
				"unsortedDoubles": []interface{}{float64(2), float64(1)},
				"emptyDoubles":    []interface{}{},

				"intBackedDoubles":          []interface{}{int64(1), int64(2), int64(2), int64(3)},
				"unsortedIntBackedDDoubles": []interface{}{int64(2), int64(1)},
				"emptyIntBackedDDoubles":    []interface{}{},

				"durations":         []interface{}{"1s", "1m", "1m", "1h"},
				"unsortedDurations": []interface{}{"1m", "1s"},
				"emptyDurations":    []interface{}{},

				"strings":         []interface{}{"a", "b", "b", "c"},
				"unsortedStrings": []interface{}{"b", "a"},
				"emptyStrings":    []interface{}{},

				"dates":         []interface{}{"2000-01-01", "2000-02-01", "2000-02-01", "2010-01-01"},
				"unsortedDates": []interface{}{"2000-02-01", "2000-01-01"},
				"emptyDates":    []interface{}{},

				"objs": []interface{}{
					map[string]interface{}{"f1": "a", "f2": "a"},
					map[string]interface{}{"f1": "a", "f2": "b"},
					map[string]interface{}{"f1": "a", "f2": "b"},
					map[string]interface{}{"f1": "a", "f2": "c"},
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"ints":         listType(&integerType),
				"unsortedInts": listType(&integerType),
				"emptyInts":    listType(&integerType),

				"doubles":         listType(&doubleType),
				"unsortedDoubles": listType(&doubleType),
				"emptyDoubles":    listType(&doubleType),

				"intBackedDoubles":          listType(&doubleType),
				"unsortedIntBackedDDoubles": listType(&doubleType),
				"emptyIntBackedDDoubles":    listType(&doubleType),

				"durations":         listType(&durationFormat),
				"unsortedDurations": listType(&durationFormat),
				"emptyDurations":    listType(&durationFormat),

				"strings":         listType(&stringType),
				"unsortedStrings": listType(&stringType),
				"emptyStrings":    listType(&stringType),

				"dates":         listType(&dateFormat),
				"unsortedDates": listType(&dateFormat),
				"emptyDates":    listType(&dateFormat),

				"objs": listType(objectTypePtr(map[string]schema.Structural{
					"f1": stringType,
					"f2": stringType,
				})),
			}),
			valid: []string{
				"self.ints.sum() == 8",
				"self.ints.min() == 1",
				"self.ints.max() == 3",
				"self.emptyInts.sum() == 0",
				"self.ints.isSorted()",
				"self.emptyInts.isSorted()",
				"self.unsortedInts.isSorted() == false",
				"self.ints.indexOf(2) == 1",
				"self.ints.lastIndexOf(2) == 2",
				"self.ints.indexOf(10) == -1",
				"self.ints.lastIndexOf(10) == -1",

				"self.doubles.sum() == 8.0",
				"self.doubles.min() == 1.0",
				"self.doubles.max() == 3.0",
				"self.emptyDoubles.sum() == 0.0",
				"self.doubles.isSorted()",
				"self.emptyDoubles.isSorted()",
				"self.unsortedDoubles.isSorted() == false",
				"self.doubles.indexOf(2.0) == 1",
				"self.doubles.lastIndexOf(2.0) == 2",
				"self.doubles.indexOf(10.0) == -1",
				"self.doubles.lastIndexOf(10.0) == -1",

				"self.intBackedDoubles.sum() == 8.0",
				"self.intBackedDoubles.min() == 1.0",
				"self.intBackedDoubles.max() == 3.0",
				"self.emptyIntBackedDDoubles.sum() == 0.0",
				"self.intBackedDoubles.isSorted()",
				"self.emptyDoubles.isSorted()",
				"self.unsortedIntBackedDDoubles.isSorted() == false",
				"self.intBackedDoubles.indexOf(2.0) == 1",
				"self.intBackedDoubles.lastIndexOf(2.0) == 2",
				"self.intBackedDoubles.indexOf(10.0) == -1",
				"self.intBackedDoubles.lastIndexOf(10.0) == -1",

				"self.durations.sum() == duration('1h2m1s')",
				"self.durations.min() == duration('1s')",
				"self.durations.max() == duration('1h')",
				"self.emptyDurations.sum() == duration('0')",
				"self.durations.isSorted()",
				"self.emptyDurations.isSorted()",
				"self.unsortedDurations.isSorted() == false",
				"self.durations.indexOf(duration('1m')) == 1",
				"self.durations.lastIndexOf(duration('1m')) == 2",
				"self.durations.indexOf(duration('2m')) == -1",
				"self.durations.lastIndexOf(duration('2m')) == -1",

				"self.strings.min() == 'a'",
				"self.strings.max() == 'c'",
				"self.strings.isSorted()",
				"self.emptyStrings.isSorted()",
				"self.unsortedStrings.isSorted() == false",
				"self.strings.indexOf('b') == 1",
				"self.strings.lastIndexOf('b') == 2",
				"self.strings.indexOf('x') == -1",
				"self.strings.lastIndexOf('x') == -1",

				"self.dates.min() == timestamp('2000-01-01T00:00:00.000Z')",
				"self.dates.max() == timestamp('2010-01-01T00:00:00.000Z')",
				"self.dates.isSorted()",
				"self.emptyDates.isSorted()",
				"self.unsortedDates.isSorted() == false",
				"self.dates.indexOf(timestamp('2000-02-01T00:00:00.000Z')) == 1",
				"self.dates.lastIndexOf(timestamp('2000-02-01T00:00:00.000Z')) == 2",
				"self.dates.indexOf(timestamp('2005-02-01T00:00:00.000Z')) == -1",
				"self.dates.lastIndexOf(timestamp('2005-02-01T00:00:00.000Z')) == -1",

				// array, map and object types use structural equality (aka "deep equals")
				"[[1], [2]].indexOf([1]) == 0",
				"[{'a': 1}, {'b': 2}].lastIndexOf({'b': 2}) == 1",
				"self.objs.indexOf(self.objs[1]) == 1",
				"self.objs.lastIndexOf(self.objs[1]) == 2",

				// avoiding empty list error with min and max by appending an acceptable default minimum value
				"([0] + self.emptyInts).min() == 0",

				// handle CEL's dynamic dispatch appropriately (special cases to handle an empty list)
				"dyn([]).sum() == 0",
				"dyn([1, 2]).sum() == 3",
				"dyn([1.0, 2.0]).sum() == 3.0",

				"[].sum() == 0", // An empty list returns an 0 int
			},
			errors: map[string]string{
				// return an error for min/max on empty list
				"self.emptyInts.min() == 1":      "min called on empty list",
				"self.emptyInts.max() == 3":      "max called on empty list",
				"self.emptyDoubles.min() == 1.0": "min called on empty list",
				"self.emptyDoubles.max() == 3.0": "max called on empty list",
				"self.emptyStrings.min() == 'a'": "min called on empty list",
				"self.emptyStrings.max() == 'c'": "max called on empty list",

				// only allow sum on numeric types and duration
				"['a', 'b'].sum() == 'c'": "found no matching overload for 'sum' applied to 'list(string).()", // compiler type checking error

				// only allow min/max/indexOf/lastIndexOf on comparable types
				"[[1], [2]].min() == [1]":                "found no matching overload for 'min' applied to 'list(list(int)).()",        // compiler type checking error
				"[{'a': 1}, {'b': 2}].max() == {'b': 2}": "found no matching overload for 'max' applied to 'list(map(string, int)).()", // compiler type checking error
			},
		},
		{name: "stdlib regex functions",
			obj: map[string]interface{}{
				"str": "this is a 123 string 456",
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"str": stringType,
			}),
			valid: []string{
				"self.str.find('[0-9]+') == '123'",
				"self.str.find('[0-9]+') != '456'",
				"self.str.find('xyz') == ''",

				"self.str.findAll('[0-9]+') == ['123', '456']",
				"self.str.findAll('[0-9]+', 0) == []",
				"self.str.findAll('[0-9]+', 1) == ['123']",
				"self.str.findAll('[0-9]+', 2) == ['123', '456']",
				"self.str.findAll('[0-9]+', 3) == ['123', '456']",
				"self.str.findAll('[0-9]+', -1) == ['123', '456']",
				"self.str.findAll('xyz') == []",
				"self.str.findAll('xyz', 1) == []",
			},
			errors: map[string]string{
				// Invalid regex with a string constant regex pattern is compile time error
				"self.str.find(')') == ''":       "compile error: program instantiation failed: error parsing regexp: unexpected ): `)`",
				"self.str.findAll(')') == []":    "compile error: program instantiation failed: error parsing regexp: unexpected ): `)`",
				"self.str.findAll(')', 1) == []": "compile error: program instantiation failed: error parsing regexp: unexpected ): `)`",
			},
		},
		{name: "URL parsing",
			obj: map[string]interface{}{
				"url": "https://user:pass@kubernetes.io:80/docs/home?k1=a&k2=b&k2=c#anchor",
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"url": stringType,
			}),
			valid: []string{
				"url('/path').getScheme() == ''",
				"url('https://example.com/').getScheme() == 'https'",
				"url('https://example.com:80/').getHost() == 'example.com:80'",
				"url('https://example.com/').getHost() == 'example.com'",
				"url('https://[::1]:80/').getHost() == '[::1]:80'",
				"url('https://[::1]/').getHost() == '[::1]'",
				"url('/path').getHost() == ''",
				"url('https://example.com:80/').getHostname() == 'example.com'",
				"url('https://127.0.0.1/').getHostname() == '127.0.0.1'",
				"url('https://[::1]/').getHostname() == '::1'",
				"url('/path').getHostname() == ''",
				"url('https://example.com:80/').getPort() == '80'",
				"url('https://example.com/').getPort() == ''",
				"url('/path').getPort() == ''",
				"url('https://example.com/path').getEscapedPath() == '/path'",
				"url('https://example.com/with space/').getEscapedPath() == '/with%20space/'",
				"url('https://example.com').getEscapedPath() == ''",
				"url('https://example.com/path?k1=a&k2=b&k2=c').getQuery() == { 'k1': ['a'], 'k2': ['b', 'c']}",
				"url('https://example.com/path?key with spaces=value with spaces').getQuery() == { 'key with spaces': ['value with spaces']}",
				"url('https://example.com/path?').getQuery() == {}",
				"url('https://example.com/path').getQuery() == {}",

				// test with string input
				"url(self.url).getScheme() == 'https'",
				"url(self.url).getHost() == 'kubernetes.io:80'",
				"url(self.url).getHostname() == 'kubernetes.io'",
				"url(self.url).getPort() == '80'",
				"url(self.url).getEscapedPath() == '/docs/home'",
				"url(self.url).getQuery() == {'k1': ['a'], 'k2': ['b', 'c']}",

				"isURL('https://user:pass@example.com:80/path?query=val#fragment')",
				"isURL('/path') == true",
				"isURL('https://a:b:c/') == false",
				"isURL('../relative-path') == false",
			},
		},
		{name: "transition rules",
			obj: map[string]interface{}{
				"v": "new",
			},
			oldObj: map[string]interface{}{
				"v": "old",
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"v": stringType,
			}),
			valid: []string{
				"oldSelf.v != self.v",
				"oldSelf.v == 'old' && self.v == 'new'",
			},
		},
		{name: "skipped transition rule for nil old primitive",
			expectSkipped: true,
			obj:           "exists",
			oldObj:        nil,
			schema:        &stringType,
			valid: []string{
				"oldSelf == self",
			},
		},
		{name: "skipped transition rule for nil old array",
			expectSkipped: true,
			obj:           []interface{}{},
			oldObj:        nil,
			schema:        listTypePtr(&stringType),
			valid: []string{
				"oldSelf == self",
			},
		},
		{name: "skipped transition rule for nil old object",
			expectSkipped: true,
			obj:           map[string]interface{}{"f": "exists"},
			oldObj:        nil,
			schema: objectTypePtr(map[string]schema.Structural{
				"f": stringType,
			}),
			valid: []string{
				"oldSelf.f == self.f",
			},
		},
		{name: "skipped transition rule for old with non-nil interface but nil value",
			expectSkipped: true,
			obj:           []interface{}{},
			oldObj:        nilInterfaceOfStringSlice(),
			schema:        listTypePtr(&stringType),
			valid: []string{
				"oldSelf == self",
			},
		},
		{name: "authorizer is not supported for CRD Validation Rules",
			obj:    []interface{}{},
			oldObj: []interface{}{},
			schema: objectTypePtr(map[string]schema.Structural{}),
			errors: map[string]string{
				"authorizer.path('/healthz').check('get').isAllowed()": "undeclared reference to 'authorizer'",
			},
		},
	}

	for i := range tests {
		i := i
		t.Run(tests[i].name, func(t *testing.T) {
			t.Parallel()
			tt := tests[i]
			tt.costBudget = celconfig.RuntimeCELCostBudget
			ctx := context.TODO()
			for j := range tt.valid {
				validRule := tt.valid[j]
				testName := validRule
				if len(testName) > 127 {
					testName = testName[:127]
				}
				t.Run(testName, func(t *testing.T) {
					t.Parallel()
					s := withRule(*tt.schema, validRule)
					celValidator := validator(&s, tt.isRoot, model.SchemaDeclType(&s, tt.isRoot), celconfig.PerCallLimit)
					if celValidator == nil {
						t.Fatal("expected non nil validator")
					}
					errs, remainingBudget := celValidator.Validate(ctx, field.NewPath("root"), &s, tt.obj, tt.oldObj, tt.costBudget)
					for _, err := range errs {
						t.Errorf("unexpected error: %v", err)
					}
					if tt.expectSkipped {
						// Skipped validations should have no cost. The only possible false positive here would be the CEL expression 'true'.
						if remainingBudget != tt.costBudget {
							t.Errorf("expected no cost expended for skipped validation, but got %d remaining from %d budget", remainingBudget, tt.costBudget)
						}
						return
					}
				})
			}
			for rule, expectErrToContain := range tt.errors {
				testName := rule
				if len(testName) > 127 {
					testName = testName[:127]
				}
				t.Run(testName, func(t *testing.T) {
					s := withRule(*tt.schema, rule)
					celValidator := NewValidator(&s, true, celconfig.PerCallLimit)
					if celValidator == nil {
						t.Fatal("expected non nil validator")
					}
					errs, _ := celValidator.Validate(ctx, field.NewPath("root"), &s, tt.obj, tt.oldObj, tt.costBudget)
					if len(errs) == 0 {
						t.Error("expected validation errors but got none")
					}
					for _, err := range errs {
						if err.Type != field.ErrorTypeInvalid || !strings.Contains(err.Error(), expectErrToContain) {
							t.Errorf("expected error to contain '%s', but got: %v", expectErrToContain, err)
						}
					}
				})
			}
		})
	}
}

// TestValidationExpressionsInSchema tests CEL integration with custom resource values and OpenAPIv3 for cases
// where the validation rules are defined at any level within the schema.
func TestValidationExpressionsAtSchemaLevels(t *testing.T) {
	tests := []struct {
		name   string
		schema *schema.Structural
		obj    interface{}
		oldObj interface{}
		errors []string // strings that error message must contain
	}{
		{name: "invalid rule under array items",
			obj: map[string]interface{}{
				"f": []interface{}{1},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"f": listType(cloneWithRule(&integerType, "self == 'abc'")),
			}),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		{name: "invalid rule under array items, parent has rule",
			obj: map[string]interface{}{
				"f": []interface{}{1},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"f": withRule(listType(cloneWithRule(&integerType, "self == 'abc'")), "1 == 1"),
			}),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		{name: "invalid rule under additionalProperties",
			obj: map[string]interface{}{
				"f": map[string]interface{}{"k": 1},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"f": mapType(cloneWithRule(&integerType, "self == 'abc'")),
			}),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		{name: "invalid rule under additionalProperties, parent has rule",
			obj: map[string]interface{}{
				"f": map[string]interface{}{"k": 1},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"f": withRule(mapType(cloneWithRule(&integerType, "self == 'abc'")), "1 == 1"),
			}),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		{name: "invalid rule under unescaped field name",
			obj: map[string]interface{}{
				"f": map[string]interface{}{
					"m": 1,
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"f": withRule(objectType(map[string]schema.Structural{"m": integerType}), "self.m == 'abc'"),
			}),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		{name: "invalid rule under unescaped field name, parent has rule",
			obj: map[string]interface{}{
				"f": map[string]interface{}{
					"m": 1,
				},
			},
			schema: withRulePtr(objectTypePtr(map[string]schema.Structural{
				"f": withRule(objectType(map[string]schema.Structural{"m": integerType}), "self.m == 'abc'"),
			}), "1 == 1"),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		// check that escaped field names do not impact CEL rule validation
		{name: "invalid rule under escaped field name",
			obj: map[string]interface{}{
				"f/2": map[string]interface{}{
					"m": 1,
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"f/2": withRule(objectType(map[string]schema.Structural{"m": integerType}), "self.m == 'abc'"),
			}),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		{name: "invalid rule under escaped field name, parent has rule",
			obj: map[string]interface{}{
				"f/2": map[string]interface{}{
					"m": 1,
				},
			},
			schema: withRulePtr(objectTypePtr(map[string]schema.Structural{
				"f/2": withRule(objectType(map[string]schema.Structural{"m": integerType}), "self.m == 'abc'"),
			}), "1 == 1"),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		{name: "failing rule under escaped field name",
			obj: map[string]interface{}{
				"f/2": map[string]interface{}{
					"m": 1,
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"f/2": withRule(objectType(map[string]schema.Structural{"m": integerType}), "self.m == 2"),
			}),
			errors: []string{"Invalid value: \"object\": failed rule: self.m == 2"},
		},
		// unescapable field names that are not accessed by the CEL rule are allowed and should not impact CEL rule validation
		{name: "invalid rule under unescapable field name",
			obj: map[string]interface{}{
				"a@b": map[string]interface{}{
					"m": 1,
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"a@b": withRule(objectType(map[string]schema.Structural{"m": integerType}), "self.m == 'abc'"),
			}),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		{name: "invalid rule under unescapable field name, parent has rule",
			obj: map[string]interface{}{
				"f@2": map[string]interface{}{
					"m": 1,
				},
			},
			schema: withRulePtr(objectTypePtr(map[string]schema.Structural{
				"f@2": withRule(objectType(map[string]schema.Structural{"m": integerType}), "self.m == 'abc'"),
			}), "1 == 1"),
			errors: []string{"found no matching overload for '_==_' applied to '(int, string)"},
		},
		{name: "failing rule under unescapable field name",
			obj: map[string]interface{}{
				"a@b": map[string]interface{}{
					"m": 1,
				},
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"a@b": withRule(objectType(map[string]schema.Structural{"m": integerType}), "self.m == 2"),
			}),
			errors: []string{"Invalid value: \"object\": failed rule: self.m == 2"},
		},
		{name: "matchExpressions - 'values' must be specified when 'operator' is 'In' or 'NotIn'",
			obj: map[string]interface{}{
				"matchExpressions": []interface{}{
					map[string]interface{}{
						"key":      "tier",
						"operator": "In",
						"values":   []interface{}{},
					},
				},
			},
			schema: genMatchSelectorSchema(`self.matchExpressions.all(rule, (rule.operator != "In" && rule.operator != "NotIn") || ((has(rule.values) && size(rule.values) > 0)))`),
			errors: []string{"failed rule"},
		},
		{name: "matchExpressions - 'values' may not be specified when 'operator' is 'Exists' or 'DoesNotExist'",
			obj: map[string]interface{}{
				"matchExpressions": []interface{}{
					map[string]interface{}{
						"key":      "tier",
						"operator": "Exists",
						"values":   []interface{}{"somevalue"},
					},
				},
			},
			schema: genMatchSelectorSchema(`self.matchExpressions.all(rule, (rule.operator != "Exists" && rule.operator != "DoesNotExist") || ((!has(rule.values) || size(rule.values) == 0)))`),
			errors: []string{"failed rule"},
		},
		{name: "matchExpressions - invalid selector operator",
			obj: map[string]interface{}{
				"matchExpressions": []interface{}{
					map[string]interface{}{
						"key":      "tier",
						"operator": "badop",
						"values":   []interface{}{},
					},
				},
			},
			schema: genMatchSelectorSchema(`self.matchExpressions.all(rule, rule.operator == "In" || rule.operator == "NotIn" || rule.operator == "DoesNotExist")`),
			errors: []string{"failed rule"},
		},
		{name: "matchExpressions - invalid label value",
			obj: map[string]interface{}{
				"matchExpressions": []interface{}{
					map[string]interface{}{
						"key":      "badkey!",
						"operator": "Exists",
						"values":   []interface{}{},
					},
				},
			},
			schema: genMatchSelectorSchema(`self.matchExpressions.all(rule, size(rule.key) <= 63 && rule.key.matches("^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$"))`),
			errors: []string{"failed rule"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.TODO()
			celValidator := validator(tt.schema, true, model.SchemaDeclType(tt.schema, true), celconfig.PerCallLimit)
			if celValidator == nil {
				t.Fatal("expected non nil validator")
			}
			errs, _ := celValidator.Validate(ctx, field.NewPath("root"), tt.schema, tt.obj, tt.oldObj, math.MaxInt)
			unmatched := map[string]struct{}{}
			for _, e := range tt.errors {
				unmatched[e] = struct{}{}
			}
			for _, err := range errs {
				if err.Type != field.ErrorTypeInvalid {
					t.Errorf("expected only ErrorTypeInvalid errors, but got: %v", err)
					continue
				}
				matched := false
				for expected := range unmatched {
					if strings.Contains(err.Error(), expected) {
						delete(unmatched, expected)
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("expected error to contain one of %v, but got: %v", unmatched, err)
				}
			}
			if len(unmatched) > 0 {
				t.Errorf("expected errors %v", unmatched)
			}
		})
	}
}

func genMatchSelectorSchema(rule string) *schema.Structural {
	s := withRule(objectType(map[string]schema.Structural{
		"matchExpressions": listType(objectTypePtr(map[string]schema.Structural{
			"key":      stringType,
			"operator": stringType,
			"values":   listType(&stringType),
		})),
	}), rule)
	return &s
}

func TestCELValidationLimit(t *testing.T) {
	tests := []struct {
		name   string
		schema *schema.Structural
		obj    interface{}
		valid  []string
	}{
		{
			name:   "test limit",
			obj:    objs(math.MaxInt64),
			schema: schemas(integerType),
			valid: []string{
				"self.val1 > 0",
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			for j := range tt.valid {
				validRule := tt.valid[j]
				t.Run(validRule, func(t *testing.T) {
					t.Parallel()
					s := withRule(*tt.schema, validRule)
					celValidator := validator(&s, false, model.SchemaDeclType(&s, false), celconfig.PerCallLimit)

					// test with cost budget exceeded
					errs, _ := celValidator.Validate(ctx, field.NewPath("root"), &s, tt.obj, nil, 0)
					var found bool
					for _, err := range errs {
						if err.Type == field.ErrorTypeInvalid && strings.Contains(err.Error(), "validation failed due to running out of cost budget, no further validation rules will be run") {
							found = true
						} else {
							t.Errorf("unexpected err: %v", err)
						}
					}
					if !found {
						t.Errorf("expect cost limit exceed err but did not find")
					}
					if len(errs) > 1 {
						t.Errorf("expect to only return cost budget exceed err once but got: %v", len(errs))
					}

					// test with PerCallLimit exceeded
					found = false
					celValidator = NewValidator(&s, true, 0)
					if celValidator == nil {
						t.Fatal("expected non nil validator")
					}
					errs, _ = celValidator.Validate(ctx, field.NewPath("root"), &s, tt.obj, nil, celconfig.RuntimeCELCostBudget)
					for _, err := range errs {
						if err.Type == field.ErrorTypeInvalid && strings.Contains(err.Error(), "no further validation rules will be run due to call cost exceeds limit for rule") {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expect PerCostLimit exceed err but did not find")
					}
				})
			}
		})
	}

}

func TestCELValidationContextCancellation(t *testing.T) {
	items := make([]interface{}, 1000)
	for i := int64(0); i < 1000; i++ {
		items[i] = i
	}
	tests := []struct {
		name   string
		schema *schema.Structural
		obj    map[string]interface{}
		rule   string
	}{
		{name: "test cel validation with context cancellation",
			obj: map[string]interface{}{
				"array": items,
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"array": listType(&integerType),
			}),
			rule: "self.array.map(e, e * 20).filter(e, e > 50).exists(e, e == 60)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.TODO()
			s := withRule(*tt.schema, tt.rule)
			celValidator := NewValidator(&s, true, celconfig.PerCallLimit)
			if celValidator == nil {
				t.Fatal("expected non nil validator")
			}
			errs, _ := celValidator.Validate(ctx, field.NewPath("root"), &s, tt.obj, nil, celconfig.RuntimeCELCostBudget)
			for _, err := range errs {
				t.Errorf("unexpected error: %v", err)
			}

			// test context cancellation
			found := false
			evalCtx, cancel := context.WithTimeout(ctx, time.Microsecond)
			cancel()
			errs, _ = celValidator.Validate(evalCtx, field.NewPath("root"), &s, tt.obj, nil, celconfig.RuntimeCELCostBudget)
			for _, err := range errs {
				if err.Type == field.ErrorTypeInvalid && strings.Contains(err.Error(), "operation interrupted") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expect operation interrupted err but did not find")
			}
		})
	}
}

// This is the most recursive operations we expect to be able to include in an expression.
// This number could get larger with more improvements in the grammar or ANTLR stack, but should *never* decrease or previously valid expressions could be treated as invalid.
const maxValidDepth = 243

// TestCELMaxRecursionDepth tests CEL setting for maxRecursionDepth.
func TestCELMaxRecursionDepth(t *testing.T) {
	tests := []struct {
		name          string
		schema        *schema.Structural
		obj           interface{}
		oldObj        interface{}
		valid         []string
		errors        map[string]string // rule -> string that error message must contain
		costBudget    int64
		isRoot        bool
		expectSkipped bool
	}{
		{name: "test CEL maxRecursionDepth",
			obj:    objs(true),
			schema: schemas(booleanType),
			valid: []string{
				strings.Repeat("self.val1"+" == ", maxValidDepth-1) + "self.val1",
			},
			errors: map[string]string{
				strings.Repeat("self.val1"+" == ", maxValidDepth) + "self.val1": "max recursion depth exceeded",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.costBudget = celconfig.RuntimeCELCostBudget
			ctx := context.TODO()
			for j := range tt.valid {
				validRule := tt.valid[j]
				testName := validRule
				t.Run(testName, func(t *testing.T) {
					t.Parallel()
					s := withRule(*tt.schema, validRule)
					celValidator := validator(&s, tt.isRoot, model.SchemaDeclType(&s, tt.isRoot), celconfig.PerCallLimit)
					if celValidator == nil {
						t.Fatal("expected non nil validator")
					}
					errs, remainingBudget := celValidator.Validate(ctx, field.NewPath("root"), &s, tt.obj, tt.oldObj, tt.costBudget)
					for _, err := range errs {
						t.Errorf("unexpected error: %v", err)
					}
					if tt.expectSkipped {
						// Skipped validations should have no cost. The only possible false positive here would be the CEL expression 'true'.
						if remainingBudget != tt.costBudget {
							t.Errorf("expected no cost expended for skipped validation, but got %d remaining from %d budget", remainingBudget, tt.costBudget)
						}
						return
					}
				})
			}
			for rule, expectErrToContain := range tt.errors {
				testName := rule
				if len(testName) > 127 {
					testName = testName[:127]
				}
				t.Run(testName, func(t *testing.T) {
					s := withRule(*tt.schema, rule)
					celValidator := NewValidator(&s, true, celconfig.PerCallLimit)
					if celValidator == nil {
						t.Fatal("expected non nil validator")
					}
					errs, _ := celValidator.Validate(ctx, field.NewPath("root"), &s, tt.obj, tt.oldObj, tt.costBudget)
					if len(errs) == 0 {
						t.Error("expected validation errors but got none")
					}
					for _, err := range errs {
						if err.Type != field.ErrorTypeInvalid || !strings.Contains(err.Error(), expectErrToContain) {
							t.Errorf("expected error to contain '%s', but got: %v", expectErrToContain, err)
						}
					}
				})
			}
		})
	}
}

func BenchmarkCELValidationWithContext(b *testing.B) {
	items := make([]interface{}, 1000)
	for i := int64(0); i < 1000; i++ {
		items[i] = i
	}
	tests := []struct {
		name   string
		schema *schema.Structural
		obj    map[string]interface{}
		rule   string
	}{
		{name: "benchmark for cel validation with context",
			obj: map[string]interface{}{
				"array": items,
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"array": listType(&integerType),
			}),
			rule: "self.array.map(e, e * 20).filter(e, e > 50).exists(e, e == 60)",
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			ctx := context.TODO()
			s := withRule(*tt.schema, tt.rule)
			celValidator := NewValidator(&s, true, celconfig.PerCallLimit)
			if celValidator == nil {
				b.Fatal("expected non nil validator")
			}
			for i := 0; i < b.N; i++ {
				errs, _ := celValidator.Validate(ctx, field.NewPath("root"), &s, tt.obj, nil, celconfig.RuntimeCELCostBudget)
				for _, err := range errs {
					b.Fatalf("validation failed: %v", err)
				}
			}
		})
	}
}

func BenchmarkCELValidationWithCancelledContext(b *testing.B) {
	items := make([]interface{}, 1000)
	for i := int64(0); i < 1000; i++ {
		items[i] = i
	}
	tests := []struct {
		name   string
		schema *schema.Structural
		obj    map[string]interface{}
		rule   string
	}{
		{name: "benchmark for cel validation with context",
			obj: map[string]interface{}{
				"array": items,
			},
			schema: objectTypePtr(map[string]schema.Structural{
				"array": listType(&integerType),
			}),
			rule: "self.array.map(e, e * 20).filter(e, e > 50).exists(e, e == 60)",
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			ctx := context.TODO()
			s := withRule(*tt.schema, tt.rule)
			celValidator := NewValidator(&s, true, celconfig.PerCallLimit)
			if celValidator == nil {
				b.Fatal("expected non nil validator")
			}
			for i := 0; i < b.N; i++ {
				evalCtx, cancel := context.WithTimeout(ctx, time.Microsecond)
				cancel()
				errs, _ := celValidator.Validate(evalCtx, field.NewPath("root"), &s, tt.obj, nil, celconfig.RuntimeCELCostBudget)
				//found := false
				//for _, err := range errs {
				//	if err.Type == field.ErrorTypeInvalid && strings.Contains(err.Error(), "operation interrupted") {
				//		found = true
				//		break
				//	}
				//}
				if len(errs) == 0 {
					b.Errorf("expect operation interrupted err but did not find")
				}
			}
		})
	}
}

// BenchmarkCELValidationWithAndWithoutOldSelfReference measures the additional cost of evaluating
// validation rules that reference "oldSelf".
func BenchmarkCELValidationWithAndWithoutOldSelfReference(b *testing.B) {
	for _, rule := range []string{
		"self.getMonth() >= 0",
		"oldSelf.getMonth() >= 0",
	} {
		b.Run(rule, func(b *testing.B) {
			obj := map[string]interface{}{
				"datetime": time.Time{}.Format(strfmt.ISO8601LocalTime),
			}
			s := &schema.Structural{
				Generic: schema.Generic{
					Type: "object",
				},
				Properties: map[string]schema.Structural{
					"datetime": {
						Generic: schema.Generic{
							Type: "string",
						},
						ValueValidation: &schema.ValueValidation{
							Format: "date-time",
						},
						Extensions: schema.Extensions{
							XValidations: []apiextensions.ValidationRule{
								{Rule: rule},
							},
						},
					},
				},
			}
			validator := NewValidator(s, true, celconfig.PerCallLimit)
			if validator == nil {
				b.Fatal("expected non nil validator")
			}

			ctx := context.TODO()
			root := field.NewPath("root")

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				errs, _ := validator.Validate(ctx, root, s, obj, obj, celconfig.RuntimeCELCostBudget)
				for _, err := range errs {
					b.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func primitiveType(typ, format string) schema.Structural {
	result := schema.Structural{
		Generic: schema.Generic{
			Type: typ,
		},
	}
	if len(format) != 0 {
		result.ValueValidation = &schema.ValueValidation{
			Format: format,
		}
	}
	return result
}

var (
	integerType = primitiveType("integer", "")
	int32Type   = primitiveType("integer", "int32")
	int64Type   = primitiveType("integer", "int64")
	numberType  = primitiveType("number", "")
	floatType   = primitiveType("number", "float")
	doubleType  = primitiveType("number", "double")
	stringType  = primitiveType("string", "")
	byteType    = primitiveType("string", "byte")
	booleanType = primitiveType("boolean", "")

	durationFormat = primitiveType("string", "duration")
	dateFormat     = primitiveType("string", "date")
	dateTimeFormat = primitiveType("string", "date-time")
)

func listType(items *schema.Structural) schema.Structural {
	return arrayType("atomic", nil, items)
}

func listTypePtr(items *schema.Structural) *schema.Structural {
	l := listType(items)
	return &l
}

func listSetType(items *schema.Structural) schema.Structural {
	return arrayType("set", nil, items)
}

func listMapType(keys []string, items *schema.Structural) schema.Structural {
	return arrayType("map", keys, items)
}

func listMapTypePtr(keys []string, items *schema.Structural) *schema.Structural {
	l := listMapType(keys, items)
	return &l
}

func arrayType(listType string, keys []string, items *schema.Structural) schema.Structural {
	result := schema.Structural{
		Generic: schema.Generic{
			Type: "array",
		},
		Extensions: schema.Extensions{
			XListType: &listType,
		},
		Items: items,
	}
	if len(keys) > 0 && listType == "map" {
		result.Extensions.XListMapKeys = keys
	}
	return result
}

func ValsEqualThemselvesAndDataLiteral(val1, val2 string, dataLiteral string) string {
	return fmt.Sprintf("%s == %s && %s == %s && %s == %s", val1, dataLiteral, dataLiteral, val1, val1, val2)
}

func objs(val ...interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(val))
	for i, v := range val {
		result[fmt.Sprintf("val%d", i+1)] = v
	}
	return result
}

func schemas(valSchema ...schema.Structural) *schema.Structural {
	result := make(map[string]schema.Structural, len(valSchema))
	for i, v := range valSchema {
		result[fmt.Sprintf("val%d", i+1)] = v
	}
	return objectTypePtr(result)
}

func objectType(props map[string]schema.Structural) schema.Structural {
	return schema.Structural{
		Generic: schema.Generic{
			Type: "object",
		},
		Properties: props,
	}
}

func objectTypePtr(props map[string]schema.Structural) *schema.Structural {
	o := objectType(props)
	return &o
}

func mapType(valSchema *schema.Structural) schema.Structural {
	result := schema.Structural{
		Generic: schema.Generic{
			Type:                 "object",
			AdditionalProperties: &schema.StructuralOrBool{Bool: true, Structural: valSchema},
		},
	}
	return result
}

func mapTypePtr(valSchema *schema.Structural) *schema.Structural {
	m := mapType(valSchema)
	return &m
}

func intOrStringType() schema.Structural {
	return schema.Structural{
		Extensions: schema.Extensions{
			XIntOrString: true,
		},
	}
}

func withRule(s schema.Structural, rule string) schema.Structural {
	s.Extensions.XValidations = apiextensions.ValidationRules{
		{
			Rule: rule,
		},
	}
	return s
}

func withRulePtr(s *schema.Structural, rule string) *schema.Structural {
	s.Extensions.XValidations = apiextensions.ValidationRules{
		{
			Rule: rule,
		},
	}
	return s
}

func cloneWithRule(s *schema.Structural, rule string) *schema.Structural {
	s = s.DeepCopy()
	return withRulePtr(s, rule)
}

func withMaxLength(s schema.Structural, maxLength *int64) schema.Structural {
	if s.ValueValidation == nil {
		s.ValueValidation = &schema.ValueValidation{}
	}
	s.ValueValidation.MaxLength = maxLength
	return s
}

func withMaxItems(s schema.Structural, maxItems *int64) schema.Structural {
	if s.ValueValidation == nil {
		s.ValueValidation = &schema.ValueValidation{}
	}
	s.ValueValidation.MaxItems = maxItems
	return s
}

func withDefault(dflt interface{}, s schema.Structural) schema.Structural {
	s.Generic.Default = schema.JSON{Object: dflt}
	return s
}

func withNullable(nullable bool, s schema.Structural) schema.Structural {
	s.Generic.Nullable = nullable
	return s
}

func withNullablePtr(nullable bool, s schema.Structural) *schema.Structural {
	s.Generic.Nullable = nullable
	return &s
}

func nilInterfaceOfStringSlice() []interface{} {
	var slice []interface{} = nil
	return slice
}
