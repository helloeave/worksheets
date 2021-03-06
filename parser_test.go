// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package worksheets

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *Zuite) TestParser_parseWorksheet() {
	cases := map[string]func(*Definition){
		`{}`: func(ws *Definition) {
			require.Equal(s.T(), "simple", ws.name)
			require.Equal(s.T(), 2+0, len(ws.fieldsByName))
			require.Equal(s.T(), 2+0, len(ws.fieldsByIndex))
		},
		`{42:full_name text}`: func(ws *Definition) {
			require.Equal(s.T(), "simple", ws.name)
			require.Equal(s.T(), 2+1, len(ws.fieldsByName))
			require.Equal(s.T(), 2+1, len(ws.fieldsByIndex))

			field := ws.fieldsByName["full_name"]
			require.Equal(s.T(), 42, field.index)
			require.Equal(s.T(), "full_name", field.name)
			require.Equal(s.T(), &TextType{}, field.typ)
			require.Equal(s.T(), ws.fieldsByName["full_name"], field)
			require.Equal(s.T(), ws.fieldsByIndex[42], field)
		},
		`{42:full_name text 45:happy bool}`: func(ws *Definition) {
			require.Equal(s.T(), "simple", ws.name)
			require.Equal(s.T(), 2+2, len(ws.fieldsByName))
			require.Equal(s.T(), 2+2, len(ws.fieldsByIndex))

			field1 := ws.fieldsByName["full_name"]
			require.Equal(s.T(), 42, field1.index)
			require.Equal(s.T(), "full_name", field1.name)
			require.Equal(s.T(), &TextType{}, field1.typ)
			require.Equal(s.T(), ws.fieldsByName["full_name"], field1)
			require.Equal(s.T(), ws.fieldsByIndex[42], field1)

			field2 := ws.fieldsByName["happy"]
			require.Equal(s.T(), 45, field2.index)
			require.Equal(s.T(), "happy", field2.name)
			require.Equal(s.T(), &BoolType{}, field2.typ)
			require.Equal(s.T(), ws.fieldsByName["happy"], field2)
			require.Equal(s.T(), ws.fieldsByIndex[45], field2)
		},
	}
	for input, checks := range cases {
		p := newParser(strings.NewReader(input))
		ws, err := p.parseWorksheet("simple")
		require.NoError(s.T(), err)
		checks(ws)
	}
}

func (s *Zuite) TestParser_parseEnum() {
	cases := map[string][]string{
		`{}`:                     nil,
		`{"foo",}`:               {"foo"},
		`{"foo","bar",}`:         {"foo", "bar"},
		`{"one","two","three",}`: {"one", "two", "three"},
		`{"hello world",}`:       {"hello world"},
	}
	for input, elements := range cases {
		var expected map[string]bool
		if len(elements) != 0 {
			expected = make(map[string]bool)
		}
		for _, element := range elements {
			expected[element] = true
		}

		p := newParser(strings.NewReader(input))
		enum, err := p.parseEnum("simple")
		require.NoError(s.T(), err, input)
		require.Equal(s.T(), "simple", enum.name)
		require.Equal(s.T(), expected, enum.elements)
		require.True(s.T(), p.isEof(), input)
	}
}

func (s *Zuite) TestParser_parseEnumErrors() {
	cases := map[string]string{
		`{`:       "expected text, found <eof>",
		`{"foo"}`: "expected ,, found }",
		`{5}`:     "expected text, found 5",
	}
	for input, expected := range cases {
		p := newParser(strings.NewReader(input))
		_, err := p.parseEnum("simple")
		require.EqualError(s.T(), err, expected)
	}
}

func (s *Zuite) TestParser_parseStatement() {
	cases := map[string]expression{
		`external`:    &tExternal{},
		`return true`: &tReturn{&Bool{true}},
	}
	for input, expected := range cases {
		p := newParser(strings.NewReader(input))
		actual, err := p.parseStatement()
		require.NoError(s.T(), err, input)
		require.Equal(s.T(), "", p.next(), "%s should have reached eof", input)
		assert.Equal(s.T(), expected, actual, input)
	}
}

func (s *Zuite) TestParser_parseExpression() {
	cases := map[string]expression{
		// literals
		`3`:         &Number{3, &NumberType{0}},
		`-5.12`:     &Number{-512, &NumberType{2}},
		`undefined`: vUndefined,
		`"Alice"`:   &Text{"Alice"},
		`true`:      &Bool{true},

		// selectors
		`foo`:         tSelector([]string{"foo"}),
		`foo.bar`:     tSelector([]string{"foo", "bar"}),
		`foo.bar.baz`: tSelector([]string{"foo", "bar", "baz"}),

		// calls
		`len(something)`: &tCall{
			tSelector([]string{"len"}),
			[]expression{tSelector([]string{"something"})},
			nil,
		},
		`first_of(undefined, 6, "Alice")`: &tCall{
			tSelector([]string{"first_of"}),
			[]expression{
				vUndefined,
				&Number{6, &NumberType{0}},
				&Text{"Alice"},
			},
			nil,
		},
		`foo.len()`: &tCall{
			tSelector([]string{"foo", "len"}),
			nil,
			nil,
		},
		`sum(len(foo))`: &tCall{
			tSelector([]string{"sum"}),
			[]expression{
				&tCall{
					tSelector([]string{"len"}),
					[]expression{
						tSelector([]string{"foo"}),
					},
					nil,
				},
			},
			nil,
		},
		`sum(len(foo),8)`: &tCall{
			tSelector([]string{"sum"}),
			[]expression{
				&tCall{
					tSelector([]string{"len"}),
					[]expression{
						tSelector([]string{"foo"}),
					},
					nil,
				},
				&Number{8, &NumberType{0}},
			},
			nil,
		},

		// calls -- allow trailing comma
		`len(5,)`: &tCall{
			tSelector([]string{"len"}),
			[]expression{
				&Number{5, &NumberType{0}},
			},
			nil,
		},
		`first_of(1,2,3,)`: &tCall{
			tSelector([]string{"first_of"}),
			[]expression{
				&Number{1, &NumberType{0}},
				&Number{2, &NumberType{0}},
				&Number{3, &NumberType{0}},
			},
			nil,
		},
		`sum(len(5,),)`: &tCall{
			tSelector([]string{"sum"}),
			[]expression{
				&tCall{
					tSelector([]string{"len"}),
					[]expression{
						&Number{5, &NumberType{0}},
					},
					nil,
				},
			},
			nil,
		},

		// calls -- with rounding
		`avg(7, 11) round half 4`: &tCall{
			tSelector([]string{"avg"}),
			[]expression{
				&Number{7, &NumberType{0}},
				&Number{11, &NumberType{0}},
			},
			&tRound{"half", 4},
		},
		`sum(1, avg(7, 11) round half 4) round up 7`: &tBinop{
			opPlus,
			&tCall{
				tSelector([]string{"sum"}),
				[]expression{
					&Number{1, &NumberType{0}},
					&tCall{
						tSelector([]string{"avg"}),
						[]expression{
							&Number{7, &NumberType{0}},
							&Number{11, &NumberType{0}},
						},
						&tRound{"half", 4},
					},
				},
				nil,
			},
			vZero,
			&tRound{"up", 7},
		},
		`avg(7, 11) round half 4 round up 7`: &tBinop{
			opPlus,
			&tCall{
				tSelector([]string{"avg"}),
				[]expression{
					&Number{7, &NumberType{0}},
					&Number{11, &NumberType{0}},
				},
				&tRound{"half", 4},
			},
			vZero,
			&tRound{"up", 7},
		},
		`sum(1, 2) / 3 round half 4`: &tBinop{
			opDiv,
			&tCall{
				tSelector([]string{"sum"}),
				[]expression{
					&Number{1, &NumberType{0}},
					&Number{2, &NumberType{0}},
				},
				nil,
			},
			&Number{3, &NumberType{0}},
			&tRound{"half", 4},
		},

		// unop and binop
		`3 + 4`: &tBinop{opPlus, &Number{3, &NumberType{0}}, &Number{4, &NumberType{0}}, nil},
		`!foo`:  &tUnop{opNot, tSelector([]string{"foo"})},

		// parentheses
		`(true)`:          &Bool{true},
		`(3 + 4)`:         &tBinop{opPlus, &Number{3, &NumberType{0}}, &Number{4, &NumberType{0}}, nil},
		`(3) + (4)`:       &tBinop{opPlus, &Number{3, &NumberType{0}}, &Number{4, &NumberType{0}}, nil},
		`((((3)) + (4)))`: &tBinop{opPlus, &Number{3, &NumberType{0}}, &Number{4, &NumberType{0}}, nil},

		// single expressions being rounded
		`3.00 round down 1`:     &tBinop{opPlus, &Number{300, &NumberType{2}}, &Number{0, &NumberType{0}}, &tRound{"down", 1}},
		`3.00 * 4 round down 5`: &tBinop{opMult, &Number{300, &NumberType{2}}, &Number{4, &NumberType{0}}, &tRound{"down", 5}},
		`3.00 round down 5 * 4`: &tBinop{
			opMult,
			&tBinop{opPlus, &Number{300, &NumberType{2}}, &Number{0, &NumberType{0}}, &tRound{"down", 5}},
			&Number{4, &NumberType{0}},
			nil,
		},

		// rounding closest to the operator it applies
		`1 * 2 round up 4 * 3 round half 5`: &tBinop{
			opMult,
			&tBinop{opMult, &Number{1, &NumberType{0}}, &Number{2, &NumberType{0}}, &tRound{"up", 4}},
			&Number{3, &NumberType{0}},
			&tRound{"half", 5},
		},
		// same way to write the above, because 1 * 2 is the first operator to
		// be folded, it associates with the first rounding mode
		`1 * 2 * 3 round up 4 round half 5`: &tBinop{
			opMult,
			&tBinop{opMult, &Number{1, &NumberType{0}}, &Number{2, &NumberType{0}}, &tRound{"up", 4}},
			&Number{3, &NumberType{0}},
			&tRound{"half", 5},
		},
		// here, because 2 / 3 is the first operator to be folded, the rounding
		// mode applies to this first
		`1 * 2 / 3 round up 4 round half 5`: &tBinop{
			opMult,
			&Number{1, &NumberType{0}},
			&tBinop{opDiv, &Number{2, &NumberType{0}}, &Number{3, &NumberType{0}}, &tRound{"up", 4}},
			&tRound{"half", 5},
		},
		// we move round up 4 closer to the 1 * 2 group, but since the division
		// has precedence, this really means that 2 is first rounded (i.e. it
		// has no bearings on the * binop)
		`1 * 2 round up 4 / 3 round half 5`: &tBinop{
			opMult,
			&Number{1, &NumberType{0}},
			&tBinop{
				opDiv,
				&tBinop{opPlus, &Number{2, &NumberType{0}}, vZero, &tRound{"up", 4}},
				&Number{3, &NumberType{0}},
				&tRound{"half", 5},
			},
			nil,
		},
	}

	for input, expected := range cases {
		p := newParser(strings.NewReader(input))
		actual, err := p.parseExpression(true)
		if assert.NoError(s.T(), err, input) {
			if assert.Equal(s.T(), "", p.next(), "%s should have reached eof", input) {
				assert.Equal(s.T(), expected, actual, input)
			}
		}
	}
}

func (s *Zuite) TestParser_parseAndEvalExprToCheckOperatorPrecedence() {
	// Parsing and evaluating expressions is an easier way to write tests for
	// operator precedence rules. It's great when things are green... And when
	// they are not, it's key to look at the AST to debug.
	cases := map[string]string{
		`3`:           `3`,
		`3 + 4`:       `7`,
		`3 + 4 + 5`:   `12`,
		`3 - 4 + 5`:   `4`,
		`3 + 4 - 5`:   `2`,
		`3 + 4 * 5`:   `23`,
		`3 * 4 + 5`:   `17`,
		`3 * (4 + 5)`: `27`,

		`1.2345 round down 0`: `1`,
		`1.2345 round down 1`: `1.2`,
		`1.2345 round down 2`: `1.23`,
		`1.2345 round down 3`: `1.234`,
		`1.2345 round down 4`: `1.2345`,
		`1.2345 round down 5`: `1.23450`,
		`1.2345 round up 0`:   `2`,
		`1.2345 round up 1`:   `1.3`,
		`1.2345 round up 2`:   `1.24`,
		`1.2345 round up 3`:   `1.235`,
		`1.2345 round up 4`:   `1.2345`,
		`1.2345 round up 5`:   `1.23450`,

		` 3 * 5  / 4 round down 0`:             `3`,
		`(3 * 5) / 4 round down 0`:             `3`,
		` 3 * 5  / 4 round up 0`:               `6`,
		`(3 * 5) / 4 round up 0`:               `4`,
		`29 / 2 round down 0 / 7 round down 0`: `2`,
		`29 / 2 round down 0 / 7 round up 0`:   `2`,
		`29 / 2 round up 0 / 7 round down 0`:   `2`,
		`29 / 2 round up 0 / 7 round up 0`:     `3`,

		`!undefined`:                       `undefined`,
		`!true`:                            `false`,
		`3 + 1 == 4`:                       `true`,
		`4 / 1 round down 0 == 2 * 2`:      `true`,
		`5 - 1 == 2 * 2 round down 0`:      `true`,
		`3 + 1 == 4 && true`:               `true`,
		`"foo" == "foo" && "bar" == "bar"`: `true`,
		`3 + 1 != 4 || true`:               `true`,
		`3 + 1 != 4 || false`:              `false`,
		`"foo" != "foo" || "bar" == "baz"`: `false`,

		`true || undefined`:                `true`,
		`true || 6 / 0 round down 7 == 6`:  `true`,
		`false && undefined`:               `false`,
		`false && 6 / 0 round down 7 == 6`: `false`,

		`15.899 > 15 + 0.8999 round down 3`:     `false`,
		`5999 / 12 round half 2 >= 499.9199999`: `true`,
		`900 - 900.111 < -0.111`:                `false`,
		`17.5 * 13 round down 0 <= 227.0`:       `true`,

		// TODO(pascal): work on convoluted examples below
		// `5 - 1 == 2 * 2 round down 2 round down 0`: `true`,
	}
	for input, output := range cases {
		expected := MustNewValue(output)
		p := newParser(strings.NewReader(input))
		expr, err := p.parseExpression(true)
		require.NoError(s.T(), err, input)
		require.Equal(s.T(), "", p.next(), "%s should have reached eof", input)
		actual, err := expr.compute(nil)
		require.NoError(s.T(), err, input)
		assert.Equal(s.T(), expected, actual, "%s should equal %s was %s", input, output, actual)
	}
}

func (s *Zuite) TestParser_parseExpressionErrors() {
	cases := map[string]string{
		`_1_234`:    "expecting expression: `_1_234` did not match patterns",
		`1_234_`:    "expecting expression: `1_234_` did not match patterns",
		`1_234.`:    "expecting expression: `1_234.` did not match patterns",
		`1_234._67`: "expecting expression: `1_234._67` did not match patterns",
		`1_234.+7`:  "expecting expression: `1_234.` did not match patterns",

		`5 round down 33`: `scale cannot be greater than 32`,
		`5 round down 9999999999999999999999999999999999999999999999999`: `scale cannot be greater than 32`,

		`len(5,`: "expecting expression: `` did not match patterns",
		`len(5!`: "expecting , or ): `!` did not match patterns",

		// will need to revisit when we implement mod operator
		`4%0`:     `number must terminate with percent if present`,
		`-1%_000`: `number must terminate with percent if present`,
		`2.7%5`:   `number must terminate with percent if present`,
		`-3%.625`: `number must terminate with percent if present`,
	}
	for input, expected := range cases {
		s.T().Run(input, func(t *testing.T) {
			p := newParser(strings.NewReader(input))
			_, err := p.parseExpression(true)
			assert.EqualError(t, err, expected, input)
		})
	}
}

func (s *Zuite) TestParser_parseNumberLiteralWithPercentAndSpace() {
	cases := map[string]Value{
		`100 %`:   &Number{100, &NumberType{0}},
		`1.625 %`: &Number{1625, &NumberType{3}},
	}
	for input, expected := range cases {
		p := newParser(strings.NewReader(input))
		actual, err := p.parseLiteral()
		require.NoError(s.T(), err, input)

		// because of space, expect that "%" token will still be in stream
		require.Equal(s.T(), "%", p.next(), "%s should not have reached eof", input)
		assert.Equal(s.T(), expected, actual, input)
	}
}

func (s *Zuite) TestParser_parseLiteral() {
	cases := map[string]Value{
		`undefined`: vUndefined,

		`1`:                  &Number{1, &NumberType{0}},
		`-123.67`:            &Number{-12367, &NumberType{2}},
		`1.000`:              &Number{1000, &NumberType{3}},
		`1_234.000_000_008`:  &Number{1234000000008, &NumberType{9}},
		`-1_234.000_000_008`: &Number{-1234000000008, &NumberType{9}},

		`6%`:         &Number{6, &NumberType{2}},
		`3.25%`:      &Number{325, &NumberType{4}},
		`-4%`:        &Number{-4, &NumberType{2}},
		`-5.666667%`: &Number{-5666667, &NumberType{8}},
		`1_50%`:      &Number{150, &NumberType{2}},
		`2_0.2%`:     &Number{202, &NumberType{3}},
		`-8_0%`:      &Number{-80, &NumberType{2}},
		`-25.3_7_5%`: &Number{-25375, &NumberType{5}},

		`"foo"`: &Text{"foo"},
		`"456"`: &Text{"456"},

		`true`: &Bool{true},
	}
	for input, expected := range cases {
		s.T().Run(input, func(t *testing.T) {
			p := newParser(strings.NewReader(input))
			actual, err := p.parseLiteral()
			require.NoError(t, err)
			assert.Equal(t, expected, actual, input)
		})
	}
}

func (s *Zuite) TestParser_parseTypeLiteral() {
	cases := map[string]Type{
		`undefined`:     &UndefinedType{},
		`text`:          &TextType{},
		`bool`:          &BoolType{},
		`number[5]`:     &NumberType{5},
		`number[32]`:    &NumberType{32},
		`[]bool`:        &SliceType{&BoolType{}},
		`[][]number[9]`: &SliceType{&SliceType{&NumberType{9}}},
		`foobar`:        &Definition{name: "foobar"},
		`FooBar`:        &Definition{name: "FooBar"},
	}
	for input, expected := range cases {
		p := newParser(strings.NewReader(input))
		actual, err := p.parseTypeLiteral()
		require.NoError(s.T(), err)
		require.Equal(s.T(), expected, actual)
	}
}

func (s *Zuite) TestParser_parseTypeLiteralErrors() {
	cases := map[string]string{
		`number[-7]`: `expected index, found -`,
		`number[33]`: `scale cannot be greater than 32`,
		`number[9999999999999999999999999999999999999999999999999]`: `scale cannot be greater than 32`,
	}
	for input, expected := range cases {
		p := newParser(strings.NewReader(input))
		_, err := p.parseTypeLiteral()
		assert.EqualError(s.T(), err, expected, input)
	}
}

func (s *Zuite) TestTokenPatterns() {
	cases := []struct {
		pattern *tokenPattern
		yes     []string
		no      []string
	}{
		{
			pName,
			[]string{"a", "a_a", "a_0", "A", "a_A", "A_a", "A_0"},
			[]string{"0", "_a", "a_", "_A", "A_"},
		},
	}
	for _, ex := range cases {
		s.T().Run(ex.pattern.name, func(t *testing.T) {
			for _, y := range ex.yes {
				assert.True(t, ex.pattern.re.MatchString(y))
			}
			for _, n := range ex.no {
				assert.False(t, ex.pattern.re.MatchString(n))
			}
		})
	}
}

func (s *Zuite) TestTokenizer() {
	cases := map[string][]string{
		`worksheet simple {1:full_name text}`: {
			"worksheet",
			"simple",
			"{",
			"1",
			":",
			"full_name",
			"text",
			"}",
		},
		`1_2___4.6_78___+_1_2`: {
			"1_2___4.6_78___",
			"+",
			"_1_2",
		},
		`1_2__6+7`: {
			"1_2__6",
			"+",
			"7",
		},
		`1_000*8%`: {
			"1_000", "*", "8%",
		},
		`5.75%*100`: {
			"5.75%", "*", "100",
		},
		`50_000 / 1.375%`: {
			"50_000", "/", "1.375%",
		},
		`0.000_100%`: {
			"0.000_100%",
		},
		`1!=2!3! =4==5=6= =7&&8&9& &0||1|2| |done`: {
			"1", "!=",
			"2", "!",
			"3", "!", "=",
			"4", "==",
			"5", "=",
			"6", "=", "=",
			"7", "&&",
			"8", "&",
			"9", "&", "&",
			"0", "||",
			"1", "|",
			"2", "|", "|",
			"done",
		},
		"1// ignore my comment\n4": {
			"1",
			"4",
		},
		`1/* this one too */4`: {
			"1",
			"4",
		},
	}
	for input, toks := range cases {
		p := newParser(strings.NewReader(input))

		for _, tok := range toks {
			require.Equal(s.T(), tok, p.next(), input)
		}
		require.Equal(s.T(), "", p.next(), input)
		require.Equal(s.T(), "", p.next(), input)
	}
}
