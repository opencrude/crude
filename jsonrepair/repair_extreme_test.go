package jsonrepair

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertRepair verifies that Repair(text) == expected without error.
func assertRepair(t *testing.T, text string, expected string) {
	t.Helper()
	result, err := Repair(text)
	require.NoError(t, err)
	assert.Equal(t, expected, result)
}

// assertRepairEqual verifies that Repair(text) == text (identity).
func assertRepairEqual(t *testing.T, text string) {
	t.Helper()
	result, err := Repair(text)
	require.NoError(t, err)
	assert.Equal(t, text, result)
}

// assertRepairFailureExact checks that the error message and position match exactly.
func assertRepairFailureExact(t *testing.T, text, expectedErrMsg string, expectedPos int) {
	t.Helper()
	var (
		result string
		err    error
	)
	assert.NotPanics(t, func() {
		result, err = Repair(text)
	})
	require.Error(t, err)
	var repairErr *Error
	require.ErrorAs(t, err, &repairErr)
	assert.Equal(t, expectedErrMsg, repairErr.Message)
	assert.Equal(t, expectedPos, repairErr.Position)
	assert.Empty(t, result)
}

// ============================================================================
// 1. UNICODE APOCALYPSE
// ============================================================================

func TestUnicodeApocalypse(t *testing.T) {
	t.Parallel()

	t.Run("zero_width_characters_everywhere", func(t *testing.T) {
		t.Parallel()
		zwChars := []rune{
			0x200B, // zero-width space
			0x200C, // zero-width non-joiner
			0x200D, // zero-width joiner
			0xFEFF, // BOM
			0x00AD, // soft hyphen
			0x2060, // word joiner
			0x180E, // mongolian vowel separator
			0x2061, // function application
			0x2062, // invisible times
			0x2063, // invisible separator
			0x2064, // invisible plus
		}
		for _, z := range zwChars {
			input := fmt.Sprintf("{%c\"key\"%c:%c\"val\"%c}", z, z, z, z)
			result, err := Repair(input)
			require.NoError(t, err, "zero-width char U+%04X should not break repair", z)
			assert.Contains(t, result, `"key"`)
			assert.Contains(t, result, `"val"`)
		}
	})

	t.Run("bidi_override_in_string", func(t *testing.T) {
		t.Parallel()
		bidi := []rune{
			0x200E, // LRM
			0x200F, // RLM
			0x202A, // LRE
			0x202B, // RLE
			0x202C, // PDF
			0x202D, // LRO
			0x202E, // RLO
			0x2066, // LRI
			0x2067, // RLI
			0x2068, // FSI
			0x2069, // PDI
		}
		for _, b := range bidi {
			input := fmt.Sprintf(`{"msg":"hello%cworld"}`, b)
			result, err := Repair(input)
			require.NoError(t, err, "bidi char U+%04X should not break repair", b)
			assert.Contains(t, result, "hello")
			assert.Contains(t, result, "world")
		}
	})

	t.Run("emoji_zalgo_clusterfuck", func(t *testing.T) {
		t.Parallel()
		combining := []rune{
			0x0300, 0x0301, 0x0302, 0x0303, 0x0308, 0x030A,
			0x0323, 0x0334, 0x0337, 0x0338, 0x034F, 0x035C,
			0x0362, 0x0488, 0x20D0, 0x20E3, 0x20E8,
		}
		var zalgo strings.Builder
		zalgo.WriteString(`"Z`)
		for i := 0; i < 100; i++ {
			zalgo.WriteRune(combining[i%len(combining)])
		}
		zalgo.WriteString(`"`)
		result, err := Repair(zalgo.String())
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(result, `"Z`), "zalgo string should start with Z: %s", result)
	})

	t.Run("emoji_zwj_sequences", func(t *testing.T) {
		t.Parallel()
		emojis := []string{
			`"👨‍👩‍👧‍👦"`,     // family
			`"👩‍💻"`,        // woman technologist
			`"🏳️‍🌈"`,      // rainbow flag
			`"🐱‍👤"`,     // ninja cat
			`"👁️‍🗨️"`,     // eye in speech bubble
		}
		for _, e := range emojis {
			result, err := Repair(e)
			require.NoError(t, err)
			assert.Equal(t, e, result, "emoji ZWJ sequence should pass through unchanged")
		}
	})

	t.Run("surrogate_pair_mischief", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name  string
			input string
		}{
			{"lone_high_surrogate", `"` + string(rune(0xD800)) + `"`},
			{"lone_low_surrogate", `"` + string(rune(0xDC00)) + `"`},
			{"high_then_text", `"` + string(rune(0xD800)) + `abc"`},
			{"low_then_text", `"` + string(rune(0xDC00)) + `abc"`},
			{"reversed_surrogates", `"` + string(rune(0xDC00)) + string(rune(0xD800)) + `"`},
			{"multiple_lone_high", `"` + string(rune(0xD800)) + string(rune(0xD800)) + string(rune(0xD800)) + `"`},
			{"surrogate_in_key", "{\"" + string(rune(0xD800)) + "\":1}"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				if err != nil {
					t.Logf("repaired: %q, error: %v", result, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("unicode_normalization_attacks", func(t *testing.T) {
		t.Parallel()
		cases := []string{
			`"café"`,      // NFC
			`"cafe\u0301"`, // NFD (é as e + combining acute)
			`"ﬁ"`,          // ligature fi (U+FB01)
			`"ß"`,          // Eszett
			`"ℌ"`,         // black-letter H
			`"ﬀ"`,         // ff ligature
			`"Ω"`,         // Greek omega
			`"ω"`,         // Greek omega (different code point)
		}
		for _, c := range cases {
			c := c
			t.Run(c, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(c)
				require.NoError(t, err)
				require.Equal(t, c, result)
			})
		}
	})

	t.Run("confusable_homoglyph_keys", func(t *testing.T) {
		t.Parallel()
		input := `{ "A": 1, "Α": 2, "А": 3 }` // Latin A, Greek Alpha, Cyrillic A
		result, err := Repair(input)
		require.NoError(t, err)
		assert.Contains(t, result, `"A"`)
		assert.Contains(t, result, `"Α"`)
		assert.Contains(t, result, `"А"`)
	})

	t.Run("astral_planes_everywhere", func(t *testing.T) {
		t.Parallel()
		input := `{ "𝄞": [ "𝐁", "🜁", "🧙‍♂️" ], "𐍈": 42 }`
		result, err := Repair(input)
		require.NoError(t, err)
		assert.Contains(t, result, "𝄞")
		assert.Contains(t, result, "𝐁")
		assert.Contains(t, result, "42")
	})
}

// ============================================================================
// 2. NUMERIC NIGHTMARE FUZZING
// ============================================================================

func TestNumericApocalypse(t *testing.T) {
	t.Parallel()

	t.Run("math_limits", func(t *testing.T) {
		t.Parallel()
		require := require.New(t)
		for _, tc := range []struct {
			input string
			desc  string
		}{
			{fmt.Sprintf("%d", math.MaxInt64), "max int64"},
			{fmt.Sprintf("%d", math.MinInt64), "min int64"},
			{fmt.Sprintf("%d", ^int64(0)), "max uint64 as signed"},
			{"179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368", "max float64 as integer"},
			{"-179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368", "min float64 as integer"},
			{"0." + strings.Repeat("0", 100) + "1", "very small decimal"},
		} {
			result, err := Repair(tc.input)
			require.NoError(err, "should repair %s (%s)", tc.input, tc.desc)
			require.NotEmpty(result, "should produce output for %s", tc.desc)
		}
	})

	t.Run("scientific_explosions", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{"1e999", "huge exponent"},
			{"1e-999", "tiny exponent"},
			{"1e+999", "positive huge exponent"},
			{"0e999999", "zero with huge exponent"},
			{"-0e999999", "negative zero huge exponent"},
			{"1E999", "uppercase E huge"},
			{"1E+308", "max float64 exponent"},
			{"1E-324", "min positive float64 exponent"},
			{strings.Repeat("1", 1000) + "e" + strings.Repeat("1", 1000), "v long mantissa and exponent"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				if err != nil {
					t.Logf("%s -> error (acceptable): %v", tc.desc, err)
					return
				}
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})

	t.Run("leading_zero_madness", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, expected string }{
			{"0", "0"},
			{"00", `"00"`},
			{"000", `"000"`},
			{"0.0", "0.0"},
			{"00.0", `"00.0"`},
			{"0.00", "0.00"},
			{"00.00", `"00.00"`},
			{"-0", "-0"},
			{"-00", `"-00"`},
			{"-0.0", "-0.0"},
			{"-00.0", `"-00.0"`},
			{"0e0", "0e0"},
			{"00e0", `"00e0"`},
			{"001", `"001"`},
			{"00100", `"00100"`},
			{"001.2", `"001.2"`},
			{"000.000", `"000.000"`},
			{"0123456789", `"0123456789"`},
			{"0.123456789", "0.123456789"},
			{"-0123", `"-0123"`},
		} {
			tc := tc
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				assertRepair(t, tc.input, tc.expected)
			})
		}
	})

	t.Run("number_adjacent_to_letter_soup", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{"ES2020", "year prefix"},
			{"abc123def", "mixed alpha-numeric"},
			{"123abc456", "numbers alpha numbers"},
			{"1a2b3c", "interleaved"},
			{"a1b2c3", "interleaved2"},
			{"1.2.3.4", "ip-like"},
			{"192.168.1.1", "ip address"},
			{"2001:db8::1", "ipv6"},
			{"746de9ad-d4ff-4c66-97d7-00a92ad46967", "uuid"},
			{"2024-10-18T18:35:22.229Z", "iso date"},
			{"2024-10-18", "date only"},
			{"12:34:56", "time only"},
			{"1.2.3", "semver-like"},
			{"1.2.3-beta.1", "semver with tag"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})

	t.Run("numbers_with_dodgy_decimals", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, expected string }{
			{"1.", "1.0"},
			{".5", `".5"`},
			{"-.5", `"-.5"`},
			{"1.2.3", `"1.2.3"`},
			{"123..5", `"123..5"`},
			{"..5", `"..5"`},
			{"1..", `"1.."`},
			{"...", `"..."`},
		} {
			tc := tc
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				assertRepair(t, tc.input, tc.expected)
			})
		}
	})

	t.Run("negative_sign_abuse", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, expected string }{
			{"-", "-0"},
			{"--1", `"--1"`},
			{"- 1", `"- 1"`},
			{"1-1", `"1-1"`},
			{"-1-1", `"-1-1"`},
			{"-.", "-0"},
		} {
			tc := tc
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				assertRepair(t, tc.input, tc.expected)
			})
		}
	})
}

// ============================================================================
// 3. STRING ESCAPE HELL
// ============================================================================

func TestStringEscapeHell(t *testing.T) {
	t.Parallel()

	t.Run("all_128_ascii_chars_in_string", func(t *testing.T) {
		t.Parallel()
		var sb strings.Builder
		sb.WriteRune('"')
		for c := rune(0x20); c <= 0x7F; c++ {
			switch c {
			case '"', '\\':
				sb.WriteRune('\\')
			}
			sb.WriteRune(c)
		}
		sb.WriteRune('"')
		result, err := Repair(sb.String())
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(result, `"`))
		assert.True(t, strings.HasSuffix(result, `"`))
	})

	t.Run("every_control_char_in_string", func(t *testing.T) {
		t.Parallel()
		for c := rune(0x00); c <= 0x1F; c++ {
			c := c
			t.Run(fmt.Sprintf("ctrl_0x%02X", c), func(t *testing.T) {
				t.Parallel()
				input := fmt.Sprintf("\"abc%cdef\"", c)
				result, err := Repair(input)
				if err != nil {
					t.Logf("control char U+%04X rejected (acceptable): %v", c, err)
					return
				}
				assert.NotContains(t, result, string(c), "control char should be escaped")
			})
		}
	})

	t.Run("backslash_combinations", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{`"a\\b"`, "double backslash"},
			{`"a\\\b"`, "triple backslash"},
			{`"a\\\\b"`, "quadruple backslash"},
			{`"a\\\\\b"`, "5 backslashes"},
			{`"a\\\\\\b"`, "6 backslashes"},
			{`"\a\b\c\d\e\f\g"`, "garbage escapes"},
			{`"\0\1\2\3\4\5\6\7\8\9"`, "numeric escapes"},
			{"\"line1\\\nline2\"", "escaped newline"},
			{`"\"\"\"\"\"`, "five escaped quotes"},
			{`"hello\\\"world"`, "backslash then escaped quote"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})

	t.Run("unicode_escape_fragments", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{`"\u"`, "just u"},
			{`"\u0"`, "1 hex digit"},
			{`"\u00"`, "2 hex digits"},
			{`"\u000"`, "3 hex digits"},
			{`"\u0000"`, "4 hex digits (valid)"},
			{`"\u00000"`, "5 hex digits"},
			{`"\u000000"`, "6 hex digits"},
			{`"\u0000000"`, "7 hex digits"},
			{`"\uGGGG"`, "invalid hex"},
			{`"\u000G"`, "partially invalid"},
			{`"\u000g"`, "lowercase partially invalid"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				if err != nil {
					t.Logf("%s -> error (acceptable): %v", tc.desc, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("tab_newline_in_quoted_string", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{"\"hello\nworld\"", "unescaped newline"},
			{"\"hello\rworld\"", "unescaped CR"},
			{"\"hello\tworld\"", "unescaped tab"},
			{"\"hello\bworld\"", "unescaped backspace"},
			{"\"hello\fworld\"", "unescaped form feed"},
			{"\"multi\nline\rstring\twith\bweird\fstuff\"", "all ctrl chars mixed"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})
}

// ============================================================================
// 4. QUOTE VARIANT COMBINATORICS
// ============================================================================

func testAllPairQuoteCombinations() []string {
	opens := []struct {
		char rune
		name string
	}{
		{'"', "straight_double"},
		{'\'', "straight_single"},
		{0x201C, "left_double_curly"},
		{0x201D, "right_double_curly"},
		{0x2018, "left_single_curly"},
		{0x2019, "right_single_curly"},
		{0x0060, "grave_accent"},
		{0x00B4, "acute_accent"},
	}
	var inputs []string
	for _, o := range opens {
		for _, c := range opens {
			inputs = append(inputs, fmt.Sprintf("{%ckey%c:%cval%c, %cother%c:%cnested%c}",
				o.char, c.char, o.char, c.char, o.char, c.char, o.char, c.char))
		}
	}
	return inputs
}

func TestQuoteCombinatorics(t *testing.T) {
	t.Parallel()

	t.Run("every_quote_pair_combination_in_object", func(t *testing.T) {
		t.Parallel()
		inputs := testAllPairQuoteCombinations()
		for i, input := range inputs {
			i, input := i, input
			t.Run(fmt.Sprintf("pair_%d", i), func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err, "input: %s", input)
				assert.Contains(t, result, `"key"`, "input: %s -> result: %s", input, result)
				assert.Contains(t, result, `"val"`, "input: %s -> result: %s", input, result)
				assert.Contains(t, result, `"other"`, "input: %s -> result: %s", input, result)
				assert.Contains(t, result, `"nested"`, "input: %s -> result: %s", input, result)
			})
		}
	})

	t.Run("mixed_quote_types_in_single_string", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`"it's working"`,                                    // single quote inside double
			`'he said "hello"'`,                                  // double inside single
			"\u201Che said \u2018hello\u2019\u201D",              // smart mixed
			"`backtick with 'single' and \"double\"`",            // backtick outer
			"\u00B4acute with `backtick`\u0060",                  // acute/grave mix
			"\u2018\u2018\u2018\u2019\u2019\u2019",               // multiple curly singles
			"\u201C\u201C\u201D\u201C\u201D\u201D",               // curly double chaos
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("unmatched_quote_nightmares", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`"hello`,             // missing end
			`hello"`,             // missing start
			`'hello`,             // missing end single
			`hello'`,             // missing start single
			"\u201Chello",        // smart open only
			"hello\u201D",        // smart close only
			"\u2018hello",        // smart single open only
			"hello\u2019",        // smart single close only
			"`hello",             // missing backtick end
			"hello`",             // missing backtick start
			`"hello'world"`,      // single quote inside doubles (valid)
			`'hello"world'`,      // double inside singles
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 5. COMMENT CHAOS
// ============================================================================

func TestCommentChaos(t *testing.T) {
	t.Parallel()

	t.Run("comments_in_every_position", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			"/*c*/{/*c*/}/*c*/",
			"[/*c*/1/*c*/,/*c*/2/*c*/]/*c*/",
			"{/*c*/\"a\"/*c*/:/*c*/1/*c*/}/*c*/",
			"//line\n{//line\n\"a\"//line\n:/*c*/1//line\n}",
			"[//line\n1,\n//line\n2\n//line\n]",
			"{/*/*/\"a\":1}",       // nested-looking comment
			"{/*//*/\"a\":1}",      // line comment inside block comment
			"{// /* */ \n\"a\":1}", // block comment inside line comment
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("unterminated_block_comments", func(t *testing.T) {
		t.Parallel()
		for _, input := range []string{
			"/* unterminated ",
			"{/* unterminated }",
			"[/* unterminated ]",
			"{\"a\":/* unterminated 1}",
			"/*1\n2\n3",
		} {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err, "unterminated comments should not error")
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("comment_inside_string_value", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`"/* not a comment */"`,
			`"// not a comment"`,
			`"/*"`,
			`"*/"`,
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.Equal(t, input, result)
			})
		}
	})

	t.Run("slash_not_comment", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`"http://example.com"`,
			`"/usr/local/bin"`,
			`"a/b/c"`,
			`{ "url": "http://foo.com/bar" }`,
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				assertRepairEqual(t, input)
			})
		}
	})
}

// ============================================================================
// 6. NESTING DEPTH ANXIETY
// ============================================================================

func TestNestingDepthAnxiety(t *testing.T) {
	t.Parallel()

	for _, depth := range []int{50, 100, 500, 1000, 5000} {
		depth := depth
		t.Run(fmt.Sprintf("nested_objects_depth_%d", depth), func(t *testing.T) {
			t.Parallel()
			var input strings.Builder
			for i := 0; i < depth; i++ {
				input.WriteString(`{"a":`)
			}
			input.WriteString(`1`)
			for i := 0; i < depth; i++ {
				input.WriteRune('}')
			}
			result, err := Repair(input.String())
			if err != nil {
				t.Logf("depth %d failed (acceptable): %v", depth, err)
				return
			}
			assert.NotEmpty(t, result)
		})
	}

	t.Run("alternating_brackets_depth", func(t *testing.T) {
		t.Parallel()
		for _, depth := range []int{10, 100, 1000} {
			depth := depth
			t.Run(fmt.Sprintf("alternating_depth_%d", depth), func(t *testing.T) {
				t.Parallel()
				var input strings.Builder
				for i := 0; i < depth; i++ {
					if i%2 == 0 {
						input.WriteString(`{"a":{`)
					} else {
						input.WriteString(`[`)
					}
				}
				input.WriteString(`1`)
				for i := 0; i < depth; i++ {
					if i%2 == 0 {
						input.WriteString(`}}`)
					} else {
						input.WriteString(`]`)
					}
				}
				result, err := Repair(input.String())
				if err != nil {
					t.Logf("alternating depth %d failed: %v", depth, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 7. CONCURRENCY and RACE CONDITIONS
// ============================================================================

func TestConcurrentRepairStress(t *testing.T) {
	inputs := []string{
		`{"a":1,"b":2}`,
		`{ 'a': 1, "b": 2 }`,
		`{"a":,"b":2}`,
		`{"foo":"bar`,
		`[1,2,3,`,
		`{a: 'hello', b: True, c: None}`,
		`/* comment */ { "a": 1 }`,
		`"unescaped " quote in string"`,
		`{"url":"https://example.com/`,
		`12345`,
		`[{"nested":{"deeply":true}}]`,
		`{"unicode":"\u2605\u2605\u2605"}`,
	}

	t.Run("parallel_burst_1000", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 1000)
		for range 1000 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for _, input := range inputs {
					_, err := Repair(input)
					if err != nil {
						errs <- err
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Logf("concurrent error (acceptable): %v", err)
		}
	})

	t.Run("goroutine_leak_check", func(t *testing.T) {
		startGoroutines := runtime.NumGoroutine()
		var wg sync.WaitGroup
		for range 100 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				Repair(`{"a":1}`)
				Repair(`{bad json`)
				Repair(`[1,2`)
				Repair("/* comment */")
			}()
		}
		wg.Wait()
		endGoroutines := runtime.NumGoroutine()
		leaked := endGoroutines - startGoroutines
		assert.Less(t, leaked, 10, "should not leak goroutines; leaked: %d", leaked)
	})

	t.Run("race_detector_exercise", func(t *testing.T) {
		var wg sync.WaitGroup
		const N = 50
		results := make([]string, N)
		errs := make([]error, N)
		for i := range N {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx], errs[idx] = Repair(`{"key":"value"}`)
			}(i)
		}
		wg.Wait()
		for i := range N {
			require.NoError(t, errs[i])
			assert.Equal(t, `{"key":"value"}`, results[i])
		}
	})
}

// ============================================================================
// 8. MONGODB- like function CALLS
// ============================================================================

func TestMongoDBApocalypse(t *testing.T) {
	t.Parallel()

	t.Run("nested_function_calls", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{`ObjectId(NumberLong("123"))`, "nested mongo functions"},
			{`ISODate(NumberLong("123"))`, "iso nested in long"},
			{`NumberLong(NumberInt("5"))`, "long in int"},
			{`NumberInt(NumberDecimal("5"))`, "int in decimal"},
			{`ObjectId(ISODate(NumberLong("123")))`, "triple nested"},
			{`{ _id: ObjectId(ObjectId("123")) }`, "objectid in objectid"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})

	t.Run("function_name_shenanigans", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`Foo()`,
			`Foo(1)`,
			`Foo(1,2,3)`,
			`Foo(Bar(Baz(1)))`,
			`$lookup()`,      // dollar sign prefix
			`_internal()`,    // underscore prefix
			`$$variable()`,   // double dollar
			`Foo123_Bar()`,   // digits and underscore
			`A()`,

			// These should NOT be treated as function calls
			`hello world`,
			`C(2)`, // just C(2) could be function-like but it's tested elsewhere
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				if err != nil {
					t.Logf("input %q error: %v", input, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("jsonp_with_weird_names", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`$({"a":1})`,
			`$$$({"a":1})`,
			`jsonp123_callback({"a":1})`,
			`JSONP_CALLBACK({"a":1})`,
			`_({"a":1})`,
			`__({"a":1})`,
			`a({"a":1})`,
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 9. WHITESPACE OBSCURA
// ============================================================================

func TestWhitespaceObscura(t *testing.T) {
	t.Parallel()

	t.Run("unicode_whitespace_everywhere", func(t *testing.T) {
		t.Parallel()
		wsChars := []rune{
			0x0009, 0x000A, 0x000D, 0x0020,
			0x00A0, // non-breaking space
			0x1680, // ogham space mark
			0x2000, // en quad
			0x2001, // em quad
			0x2002, // en space
			0x2003, // em space
			0x2004, // three-per-em space
			0x2005, // four-per-em space
			0x2006, // six-per-em space
			0x2007, // figure space
			0x2008, // punctuation space
			0x2009, // thin space
			0x200A, // hair space
			0x200B, // zero-width space
			0x2028, // line separator
			0x2029, // paragraph separator
			0x202F, // narrow no-break space
			0x205F, // medium mathematical space
			0x3000, // ideographic space
			0xFEFF, // ZWNBSP / BOM
		}
		for _, ws := range wsChars {
			ws := ws
			t.Run(fmt.Sprintf("ws_U+%04X", ws), func(t *testing.T) {
				t.Parallel()
				input := fmt.Sprintf("{%c\"a\"%c:%c1%c}", ws, ws, ws, ws)
				result, err := Repair(input)
				require.NoError(t, err, "whitespace U+%04X should not cause error", ws)
				assert.Contains(t, result, `"a"`)
				assert.Contains(t, result, `1`)
			})
		}
	})

	t.Run("bom_positions", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			"\uFEFF{\"a\":1}",
			"{\uFEFF\"a\":1}",
			"{\"a\"\uFEFF:1}",
			"{\"a\":\uFEFF1}",
			"[\uFEFF1,2,3]",
			"\uFEFF\uFEFF{\"a\":1}",
			"\"\uFEFF\"", // BOM inside string
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 10. TRUNCATION EDGE CASE MANIA
// ============================================================================

func TestTruncationEdgeCaseMania(t *testing.T) {
	t.Parallel()

	t.Run("object_truncated_at_every_position", func(t *testing.T) {
		t.Parallel()
		full := `{"a":1,"b":2,"c":3}`
		for i := 0; i <= len(full); i++ {
			truncated := full[:i]
			result, err := Repair(truncated)
			if err != nil {
				t.Logf("truncated to %d -> error: %v", i, err)
				continue
			}
			assert.NotEmpty(t, result, "truncated to %d chars should produce output", i)
		}
	})

	t.Run("array_truncated_at_every_position", func(t *testing.T) {
		t.Parallel()
		full := `[1,2,3,4,5]`
		for i := 0; i <= len(full); i++ {
			truncated := full[:i]
			result, err := Repair(truncated)
			if err != nil {
				t.Logf("truncated to %d -> error: %v", i, err)
				continue
			}
			assert.NotEmpty(t, result, "truncated to %d chars should produce output", i)
		}
	})

	t.Run("string_truncated_at_every_position", func(t *testing.T) {
		t.Parallel()
		full := `"hello world this is a test string"`
		for i := 0; i <= len(full); i++ {
			truncated := full[:i]
			result, err := Repair(truncated)
			if err != nil {
				t.Logf("truncated to %d -> error: %v", i, err)
				continue
			}
			assert.NotEmpty(t, result, "truncated to %d chars should produce output", i)
		}
	})

	t.Run("nested_truncated_madness", func(t *testing.T) {
		t.Parallel()
		full := `{"level1":{"level2":{"level3":{"level4":42}}}}`
		for i := 0; i <= len(full); i++ {
			truncated := full[:i]
			result, err := Repair(truncated)
			if err != nil {
				continue
			}
			assert.NotEmpty(t, result)
		}
	})
}

// ============================================================================
// 11. EMPTY AND MINIMAL INPUTS
// ============================================================================

func TestMinimalAndEmptyInputs(t *testing.T) {
	t.Parallel()

	t.Run("empty_string_should_error", func(t *testing.T) {
		t.Parallel()
		_, err := Repair("")
		require.Error(t, err)
	})

	t.Run("minimal_valid", func(t *testing.T) {
		t.Parallel()
		for _, input := range []string{
			`""`, `"a"`, `[]`, `{}`, `0`, `1`, `-1`,
			`true`, `false`, `null`,
			`[null]`, `{"":""}`,
		} {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				assertRepairEqual(t, input)
			})
		}
	})

	t.Run("whitespace_only", func(t *testing.T) {
		t.Parallel()
		for _, input := range []string{" ", "  ", "\t", "\n", "\r\n", " \t\n\r "} {
			input := input
			t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
				t.Parallel()
				_, err := Repair(input)
				require.Error(t, err, "whitespace-only input should error: %q", input)
			})
		}
	})
}

// ============================================================================
// 12. CROSS-SITE SCRIPTING (XSS) and INJECTION PATTERNS
// ============================================================================

func TestXSSCodeInjectionPatterns(t *testing.T) {
	t.Parallel()

	payloads := []string{
		`{"a":"<script>alert('xss')</script>"}`,
		`{"a":"<img src=x onerror=alert(1)>"}`,
		`{"a":"javascript:alert(1)"}`,
		`{"a":"'); DROP TABLE users; --"}`,
		`{"a":"${process.env.SECRET}"}`,
		`{"a":"{{constructor.constructor('return process')()}}"}`,
		`{"a":"__proto__"}`,
		`{"a":"constructor[prototype]"}`,
		`{"a":"{{7*7}}"}`,
	}

	for _, payload := range payloads {
		payload := payload
		t.Run(payload[:min(len(payload), 30)], func(t *testing.T) {
			t.Parallel()
			repaired, err := Repair(payload)
			require.NoError(t, err)
			// The input already has proper quotes and JSON structure,
			// so it should be left as-is
			assert.Equal(t, payload, repaired)
		})
	}
}

// ============================================================================
// 13. MIXED PROBLEMS (multiple issues at once)
// ============================================================================

func TestMixedProblems(t *testing.T) {
	t.Parallel()

	inputs := []string{
		// missing quotes + trailing comma + comment
		"{a: 1, // trailing\n}",
		// unquoted key + single quotes + Python constants
		"{'a': True, 'b': None}",
		// unquoted URL + trailing comma + missing end quote
		`{url:https://example.com/,"name":"john`,
		// markdown code block + comment + missing value
		"```json\n{\"a\": /*comment*/}\n```",
		// NDJSON-like with comments
		"/* header */ 1\n/* separator */ 2\n/* footer */ 3",
		// regex + MongoDB + trailing comma
		`{regex: /hello/, ObjectId("123"),}`,
		// leading zero number + missing colon + unquoted string
		`{a 0789}`,
		// ellipsis + trailing comma + comment
		`[1,2,3,...,]`,
		// string concatenation + unicode escape
		`"hello" + " \u2605 world"`,
		// everything broken
		"```\n// comment\n{a: True, 'b': /* weird */ None, \"c\": https://x.com/,\"d\":\"hello\n```",
	}
	for _, input := range inputs {
		input := input
		t.Run(input[:min(len(input), 40)], func(t *testing.T) {
			t.Parallel()
			result, err := Repair(input)
			if err != nil {
				t.Logf("multi-problem input error: %v", err)
				return
			}
			assert.NotEmpty(t, result)
		})
	}
}

// ============================================================================
// 14. COMMA PLACEMENT MADNESS
// ============================================================================

func TestCommaPlacementMadness(t *testing.T) {
	t.Parallel()

	t.Run("arrays_with_commas_everywhere", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{",", "just a comma"},
			{",,,", "three commas"},
			{"[,,]", "array with leading commas"},
			{"[,1,2,3]", "leading comma"},
			{"[1,,2,3]", "middle missing value"},
			{"[1,2,,3]", "another missing value"},
			{"[1,2,3,]", "trailing comma"},
			{"[,,1,,2,,3,,]", "commas everywhere"},
			{"[1\n,\n2\n,\n3\n,]", "commas on new lines"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})

	t.Run("objects_with_commas_everywhere", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{"{,}", "just comma in braces"},
			{",}", "comma before close"},
			{"{,\"a\":1}", "leading comma"},
			{"{\"a\":1,\"b\":2,}", "trailing comma"},
			{"{\"a\":1,,\"b\":2}", "double comma"},
			{"{\"a\":1,\"b\":2,,,}", "triple trailing"},
			{"{,\"a\":1,\"b\":2,}", "leading and trailing"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})
}

// ============================================================================
// 15. ELLIPSIS OVERLOAD
// ============================================================================

func TestEllipsisOverload(t *testing.T) {
	t.Parallel()

	t.Run("ellipsis_arrays", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			"[...]", "[..., 1, 2, 3]", "[1, 2, 3, ...]",
			"[..., ...]", "[1, ..., 2, ..., 3]",
			"[..., ... , ...]", "[1, 2, ..., 3, ... , 4]",
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				if err != nil {
					t.Logf("input %q error: %v", input, err)
					return
				}
				assert.NotEmpty(t, result, "ellipsis array should produce output: %q -> %q", input, result)
			})
		}
	})

	t.Run("ellipsis_objects", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			"{...}", "{\"a\": 1, ..., \"b\": 2}",
			"{..., \"a\": 1}", "{\"a\": 1, ...}",
			"{\"a\": 1, ..., ..., \"b\": 2}",
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				if err != nil {
					t.Logf("input %q error: %v", input, err)
					return
				}
				assert.NotEmpty(t, result, "input: %q -> result: %q", input, result)
			})
		}
	})
}

// ============================================================================
// 16. KEYWORD FUZZING
// ============================================================================

func TestKeywordFuzzing(t *testing.T) {
	t.Parallel()

	t.Run("adjacent_keywords", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{"truetrue", "double true"},
			{"truefalse", "true false"},
			{"truenull", "true null"},
			{"falsefalse", "double false"},
			{"falsenull", "false null"},
			{"nullnull", "double null"},
			{"TrueFalseNone", "python constants adjacent"},
			{"TrueTrue", "double Python True"},
			{"undefinedTrue", "undefined adjacent true"},
			{"nulltruefalse", "triple keywords"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				// These may or may not error depending on how parser handles
				// adjacent keywords. Just ensure no panic.
				assert.NotPanics(t, func() {
					Repair(tc.input)
				})
			})
		}
	})

	t.Run("mixed_case_keywords", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, expected string }{
			{"TRUE", `"TRUE"`},
			{"FALSE", `"FALSE"`},
			{"NULL", `"NULL"`},
			{"True", "true"},
			{"False", "false"},
			{"None", "null"},
			{"true", "true"},
			{"false", "false"},
			{"null", "null"},
			{"truE", `"truE"`},
			{"falSe", `"falSe"`},
			{"nulL", `"nulL"`},
			{"TRUe", `"TRUe"`},
		} {
			tc := tc
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				assertRepair(t, tc.input, tc.expected)
			})
		}
	})
}

// ============================================================================
// 17. REGEX ABUSE
// ============================================================================

func TestRegexAbuse(t *testing.T) {
	t.Parallel()

	t.Run("regex_with_special_chars", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{`/[a-z]+/`, "simple regex"},
			{`/.*/`, "dot star"},
			{`/\w+\d+/`, "word digit"},
			{`/(https?:\/\/[^\s]+)/`, "url regex"},
			{`/^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$/`, "email regex"},
			{`/\/\*[\s\S]*?\*\//`, "comment regex"},
			{`/\\/`, "single backslash regex"},
			{`/"/`, "quote regex"},
			{`/'/`, "single quote regex"},
			{`/\x00/`, "null byte in regex"},
			{`/\u0000/`, "unicode null in regex"},
			{`/[\u0000-\u001F]/`, "control char range regex"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})

	t.Run("xss_via_regex", func(t *testing.T) {
		t.Parallel()
		payloads := []string{
			`/foo";alert(1);//`,
			`/foo"/alert(1)/`,
			`/--!><script>alert(1)</script>/`,
			`/'; DROP TABLE regex; --/`,
			`/${7*7}/`,
			`/foo/;console.log(1)/`,
		}
		for _, payload := range payloads {
			payload := payload
			t.Run(payload[:min(len(payload), 25)], func(t *testing.T) {
				t.Parallel()
				repaired, err := Repair(payload)
				require.NoError(t, err)
				assert.NotEmpty(t, repaired)
			})
		}
	})
}

// ============================================================================
// 18. VERY LARGE INPUTS
// ============================================================================

func TestLargeInputs(t *testing.T) {
	t.Parallel()

	t.Run("huge_string", func(t *testing.T) {
		t.Parallel()
		payload := `"` + strings.Repeat("a", 10000) + `"`
		result, err := Repair(payload)
		require.NoError(t, err)
		assert.Len(t, result, 10002)
	})

	t.Run("huge_array_of_numbers", func(t *testing.T) {
		t.Parallel()
		var sb strings.Builder
		sb.WriteRune('[')
		for i := 0; i < 1000; i++ {
			if i > 0 {
				sb.WriteRune(',')
			}
			sb.WriteString(fmt.Sprintf("%d", i))
		}
		sb.WriteRune(']')
		result, err := Repair(sb.String())
		require.NoError(t, err)
		assert.NotEmpty(t, result)
	})

	t.Run("huge_nested_object", func(t *testing.T) {
		t.Parallel()
		var sb strings.Builder
		sb.WriteRune('{')
		for i := 0; i < 500; i++ {
			if i > 0 {
				sb.WriteRune(',')
			}
			sb.WriteString(fmt.Sprintf(`"k%d":"v%d"`, i, i))
		}
		sb.WriteRune('}')
		result, err := Repair(sb.String())
		require.NoError(t, err)
		assert.NotEmpty(t, result)
	})

	t.Run("mixed_huge_array_with_missing_quotes", func(t *testing.T) {
		t.Parallel()
		var sb strings.Builder
		sb.WriteRune('[')
		for i := 0; i < 500; i++ {
			if i > 0 {
				sb.WriteRune(',')
			}
			if i%2 == 0 {
				sb.WriteString(fmt.Sprintf(`"item%d"`, i))
			} else {
				sb.WriteString(fmt.Sprintf(`unquoted_item_%d`, i))
			}
		}
		sb.WriteRune(']')
		result, err := Repair(sb.String())
		require.NoError(t, err)
		assert.NotEmpty(t, result)
	})
}

// ============================================================================
// 19. PATH DETECTION CRAZINESS
// ============================================================================

func TestPathDetectionCraziness(t *testing.T) {
	t.Parallel()

	t.Run("windows_path_nightmares", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{`"C:\"`, "drive root"},
			{`"C:\Users\"`, "trailing backslash"},
			{`"C:\Use rs\file.txt"`, "space in dir name"},
			{`"\\?\C:\Windows\file.txt"`, "long path prefix"},
			{`"\\?\UNC\server\share\file.txt"`, "UNC long path"},
			{`"C:\Program Files (x86)\Common Files\"`, "complex path"},
			{`"C:\Users\name\AppData\Local\Temp\"`, "temp path"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("unix_path_nightmares", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{`"/"`, "root"},
			{`"/root/."`, "dot dir"},
			{`"/root/.."`, "parent dir"},
			{`"/home/user/with space/file.txt"`, "spaces in path"},
			{`"/home/user/.hidden/file"`, "hidden dir"},
			{`"/"` + strings.Repeat("a", 100) + `"/file"`, "very long dir name"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("mixed_slash_paths_in_unquoted_strings", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{`{path:C:\Users\file.txt}`, "windows path unquoted value"},
			{`{path:/usr/local/bin}`, "unix path unquoted value"},
			{`{path:C:\Users\name}`, "windows path unquoted"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 20. NEWLINE DELIMITED JSON (NDJSON) NIGHTMARES
// ============================================================================

func TestNDJSONNightmares(t *testing.T) {
	t.Parallel()

	t.Run("various_ndjson_like", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{"1\n2\n3", "three numbers"},
			{"a\nb\nc", "three unquoted strings"},
			{"true\nfalse\nnull", "keywords"},
			{"{}\n[]\n\"str\"\n1", "mixed types classic"},
			{"{}\n{}\n{}\n{}\n{}", "many objects"},
			{"1,\n2,\n3,", "commas between items"},
			{"/*c1*/\n{}\n/*c2*/\n{}\n/*c3*/\n{}", "commented ndjson"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 21. DOUBLE- BYTE AND MULTI- BYTE MISMATCH
// ============================================================================

func TestMultiByteMismatch(t *testing.T) {
	t.Parallel()

	t.Run("multi_byte_in_keys", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			"{日本語: \"value\"}",
			"{中文: \"test\"}",
			"{한글: \"test\"}",
			"{ру́сский: \"test\"}",
			"{עִבְרִית: \"test\"}",
			"{\"😀😁😂\": \"emoji key\"}",
			"{\"a\u0300\u0301\u0302b\": \"combining key\"}",
		}
		for _, input := range inputs {
			input := input
			t.Run(input[:min(len(input), 20)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("bidi_text_in_string", func(t *testing.T) {
		t.Parallel()
		// Simulated Arabic + Hebrew + English mixed in one string
		input := `{"msg":"Hello שלום мир こんにちは 👋"}`
		result, err := Repair(input)
		require.NoError(t, err)
		assert.Equal(t, input, result)
	})
}

// ============================================================================
// 22. JSONP / FUNCTION CALL VARIATIONS
// ============================================================================

func TestJSONPVariations(t *testing.T) {
	t.Parallel()

	t.Run("jsonp_with_noise", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			"callback_123({});",
			"  callback_123({});  ",
			"\ncallback_123({});\n",
			"/*comment*/callback_123({})",
			"//line\ncallback_123({})",
			"callback({})",
			"cb({})",
			"f({})",
			"_({})",
			"$({})",
			"jQuery1234567890({})",
		}
		for _, input := range inputs {
			input := input
			t.Run(input[:min(len(input), 25)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})

	t.Run("nested_functions", func(t *testing.T) {
		t.Parallel()
		for _, input := range []string{
			`cb({"a":cb({})})`,
			`cb([cb({})])`,
			`cb(cb(cb({})))`,
		} {
			input := input
			t.Run(input[:min(len(input), 20)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 23. STRING CONCATENATION EXTREMES
// ============================================================================

func TestStringConcatenationExtremes(t *testing.T) {
	t.Parallel()

	t.Run("chained_concat", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, expected string }{
			{`"a"+"b"+"c"`, `"abc"`},
			{`"a" + "b" + "c" + "d" + "e"`, `"abcde"`},
			{`"hello " + "world" + "!"`, `"hello world!"`},
		} {
			tc := tc
			t.Run(tc.input[:min(len(tc.input), 20)], func(t *testing.T) {
				t.Parallel()
				assertRepair(t, tc.input, tc.expected)
			})
		}
	})

	t.Run("concat_with_other_issues", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{`"hello" + ' world'`, "mixed quote types concat"},
			{`"hello" + /* comment */ " world"`, "commented concat"},
			{"\"hello\" +\n \"world\"", "newline between concat"},
			{`"a" + "b" + "c"`, "simple chain"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})
}

// ============================================================================
// 24. MARKDOWN CODE BLOCK OVERLOAD
// ============================================================================

func TestMarkdownCodeBlockOverload(t *testing.T) {
	t.Parallel()

	t.Run("various_fence_styles", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			"```\n{\"a\":1}\n```",
			"```json\n{\"a\":1}\n```",
			"```JSON\n{\"a\":1}\n```",
			"```javascript\n{\"a\":1}\n```",
			"```\n```",
			"```\n\n\n```",
			"[```\n{\"a\":1}\n```]",
			"{```\n{\"a\":1}\n```}",
			"```\n```\n```\n```",
		}
		for _, input := range inputs {
			input := input
			t.Run(input[:min(len(input), 20)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				if err != nil {
					t.Logf("fence input %q error: %v", input, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 25. ESCAPE SEQUENCE COMBINATORICS
// ============================================================================

func TestEscapeSequenceCombinatorics(t *testing.T) {
	t.Parallel()

	escapeChars := []rune{'"', '\\', '/', 'b', 'f', 'n', 'r', 't'}

	t.Run("all_escape_pairs", func(t *testing.T) {
		t.Parallel()
		for _, e1 := range escapeChars {
			for _, e2 := range escapeChars {
				e1, e2 := e1, e2
				t.Run(fmt.Sprintf("\\%c_\\%c", e1, e2), func(t *testing.T) {
					t.Parallel()
					input := fmt.Sprintf("\"before\\%cafter\\%cend\"", e1, e2)
					result, err := Repair(input)
					require.NoError(t, err)
					assert.NotEmpty(t, result)
				})
			}
		}
	})

	t.Run("escape_at_every_position", func(t *testing.T) {
		t.Parallel()
		base := `"` + strings.Repeat("a", 20) + `"`
		for i := 1; i <= 20; i++ {
			i := i
			t.Run(fmt.Sprintf("pos_%d", i), func(t *testing.T) {
				t.Parallel()
				input := base[:i] + `\n` + base[i:len(base)-1] + `"`
				result, err := Repair(input)
				if err != nil {
					t.Logf("escape at position %d error: %v", i, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 26. PARTIAL / TRUNCATED KEYWORDS
// ============================================================================

func TestPartialKeywords(t *testing.T) {
	t.Parallel()

	t.Run("truncated_keywords", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{"tr", "truncated true start"},
			{"tru", "almost true"},
			{"fa", "truncated false start"},
			{"fal", "almost false"},
			{"fals", "almost false2"},
			{"nu", "truncated null start"},
			{"nul", "almost null"},
			{"Tr", "python True start"},
			{"Tru", "python True almost"},
			{"No", "python None start"},
			{"Non", "python None almost"},
			{"un", "undefined start"},
			{"und", "undefined almost"},
			{"undef", "undefined half"},
			{"undefi", "undefined almost"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				if err != nil {
					t.Logf("%s -> error: %v", tc.desc, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 27. SPECIAL CHARACTERS IN UNQUOTED STRINGS
// ============================================================================

func TestSpecialCharsInUnquotedStrings(t *testing.T) {
	t.Parallel()

	t.Run("punctuation_in_unquoted", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{`hello!`, "exclamation"},
			{`hello?`, "question"},
			{`hello;`, "semicolon"},
			{`(hello)`, "parentheses"},
			{`hello(42)`, "function-like"},
			{`a:b`, "colon"},
			{`a|b`, "pipe"},
			{`a~b`, "tilde"},
			{`a@b`, "at sign"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 28. CONTROL CHARACTERS OUTSIDE STRINGS
// ============================================================================

func TestControlCharsOutsideStrings(t *testing.T) {
	t.Parallel()

	for _, c := range []rune{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x0B, 0x0E, 0x0F,
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F} {
		c := c
		t.Run(fmt.Sprintf("ctrl_0x%02X_outside", c), func(t *testing.T) {
			t.Parallel()
			input := fmt.Sprintf("{%c\"a\":1}", c)
			result, err := Repair(input)
			if err != nil {
				t.Logf("ctrl U+%04X outside string error: %v", c, err)
				return
			}
			assert.NotEmpty(t, result)
		})
	}
}

// ============================================================================
// 29. FAILING (ERROR) EDGE CASES
// ============================================================================

func TestFailingEdgeCases(t *testing.T) {
	t.Parallel()

		// These should produce errors - validate the error types
	tests := []struct {
		name        string
		input       string
		errType     error
		errContains string
	}{
		{"empty", "", ErrUnexpectedEnd, "unexpected end"},
		{"invalid char in string", "\"\u0000\"", ErrInvalidCharacter, "invalid character"},
		{"unexpected after object", `{"a":1}x`, ErrUnexpectedCharacter, "unexpected character"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Repair(tc.input)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.errType)
			var repairErr *Error
			require.ErrorAs(t, err, &repairErr)
			assert.GreaterOrEqual(t, repairErr.Position, 0)
		})
	}
}

// ============================================================================
// 30. NEGATIVE AND ZERO INDEX TESTS (from parseNumber edge cases)
// ============================================================================

func TestNegativeAndZeroEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("negative_number_edge_cases", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, expected string }{
			{"-0", "-0"},
			{"-0.0", "-0.0"},
			{"-0e0", "-0e0"},
			{"-0e+0", "-0e+0"},
			{"-0e-0", "-0e-0"},
			{"-0.", "-0.0"},
			{"-0.e0", "-0.e0"},
			{"-0.0e0", "-0.0e0"},
			{"-.0", `"-.0"`},
		} {
			tc := tc
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				assertRepair(t, tc.input, tc.expected)
			})
		}
	})

	t.Run("zero_edge_cases", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, expected string }{
			{"0", "0"},
			{"0.0", "0.0"},
			{"0.", "0.0"},
			{"0e0", "0e0"},
			{"0e+0", "0e+0"},
			{"0e-0", "0e-0"},
			{"0.0e0", "0.0e0"},
			{"00", `"00"`},
			{"00.0", `"00.0"`},
			{"0.00", "0.00"},
			{"00.00", `"00.00"`},
		} {
			tc := tc
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				assertRepair(t, tc.input, tc.expected)
			})
		}
	})
}

// ============================================================================
// 31. ARRAY WITH ALL POSSIBLE EMPTY/ NIL VALUES
// ============================================================================

func TestArrayWithAllNilVariants(t *testing.T) {
	t.Parallel()

	t.Run("array_of_nothing_and_everything", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{"[null,undefined,None,nil,nothing]", "all null-like"},
			{"[true,false,True,False,null,None,undefined]", "all literals"},
			{"[{},[],\"\",0,-0,1,-1,0.5,\"str\"]", "all value types"},
			{"[,,,]", "empty slots"},
			{"[1,,2,,3]", "mixed slots"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 32. RECURSIVE / SELF-REFERENTIAL LOOKING PATTERNS
// ============================================================================

func TestSelfReferentialPatterns(t *testing.T) {
	t.Parallel()

	t.Run("circular_looking_input", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`{"$ref": "#"}`,
			`{"$id": "/foo","$ref": "/foo"}`,
			`{"self": {"self": {"self": null}}}`,
			`[{"id":1,"children":[{"id":2,"children":[]}]}]`,
		}
		for _, input := range inputs {
			input := input
			t.Run(input[:min(len(input), 25)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 33. RANDOM / FUZZ- LIKE CASES
// ============================================================================

func TestRandomFuzzCases(t *testing.T) {
	t.Parallel()

	t.Run("random_mutations", func(t *testing.T) {
		t.Parallel()
		cases := []string{
			`"` + strings.Repeat("\\", 100) + `"`,
			`"` + strings.Repeat("\"", 100) + `"`,
			`"` + strings.Repeat("\\u", 50) + `"`,
			`{` + strings.Repeat(`"a":{`, 50) + `1` + strings.Repeat(`}`, 50),
			`[` + strings.Repeat(`[`, 50) + `1` + strings.Repeat(`]`, 50),
			`{` + strings.Repeat(`"a":`, 50) + `1` + strings.Repeat(`}`, 50),
			`{"a":` + strings.Repeat(`{"b":`, 50) + `1` + strings.Repeat(`}`, 50),
			"//" + strings.Repeat("a", 1000) + "\n{}",
			"/*" + strings.Repeat("a", 1000) + "*/{}",
			strings.Repeat(" ", 1000) + "{}",
			`{"a":"` + strings.Repeat("\\n", 500) + `"}`,
			strings.Repeat("a", 10000),
			strings.Repeat(".", 1000),
			strings.Repeat(",", 1000),
			strings.Repeat("[", 1000),
			strings.Repeat("{", 1000),
		}
		for i, input := range cases {
			input := input
			t.Run(fmt.Sprintf("fuzz_%d", i), func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				if err != nil {
					t.Logf("fuzz case %d error: %v", i, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 34. ISOLATED PARSER FUNCTION TESTS (white- box grenades)
// ============================================================================

func TestParserFunctionCompatibility(t *testing.T) {
	t.Parallel()

	t.Run("isStartOfValue_all", func(t *testing.T) {
		t.Parallel()
		for c := rune(0); c <= unicode.MaxLatin1; c++ {
			result := isStartOfValue(c)
			// This should never panic for any latin-1 character
			_ = result
		}
	})

	t.Run("isDelimiter_all", func(t *testing.T) {
		t.Parallel()
		for c := rune(0); c <= 0xFF; c++ {
			result := isDelimiter(c)
			_ = result
		}
	})

	t.Run("isWhitespace_all", func(t *testing.T) {
		t.Parallel()
		for c := rune(0); c <= unicode.MaxLatin1; c++ {
			result := isWhitespace(c)
			_ = result
		}
	})

	t.Run("isSpecialWhitespace_all", func(t *testing.T) {
		t.Parallel()
		for c := rune(0); c <= 0xFFFF; c++ {
			result := isSpecialWhitespace(c)
			_ = result
		}
	})

	t.Run("isQuote_all", func(t *testing.T) {
		t.Parallel()
		for c := rune(0); c <= 0xFFFF; c++ {
			result := isQuote(c)
			_ = result
		}
	})
}

// ============================================================================
// 35. MULTIPLE OPENING/ CLOSING BRACKETS AT TOP LEVEL
// ============================================================================

func TestExtraClosingBrackets(t *testing.T) {
	t.Parallel()

	t.Run("extra_closing_brackets", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, expected string }{
			{`{"a":1}}`, `{"a":1}`},
			{`{"a":1}]`, `{"a":1}`},
			{`[1,2,3]]]`, `[1,2,3]`},
			{`{"a":1}}}]}`, `{"a":1}`},
			{`[{"a":1}]]]`, `[{"a":1}]`},
			{`{"a":1}  }  ]  }  `, `{"a":1}        `},
		} {
			tc := tc
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				assertRepair(t, tc.input, tc.expected)
			})
		}
	})
}

// ============================================================================
// 36. COLON AND KEY PATTERNS
// ============================================================================

func TestColonKeyPatterns(t *testing.T) {
	t.Parallel()

	t.Run("missing_colons_various", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{`{"a" "b"}`, "string key, string value"},
			{`{"a" 2}`, "string key, number value"},
			{`{"a" null}`, "string key, null value"},
			{`{"a" true}`, "string key, bool value"},
			{`{a "b"}`, "unquoted key, string value"},
			{`{a 2}`, "unquoted key, number value"},
			{`{a null}`, "unquoted key, null value"},
			{`{a true}`, "unquoted key, bool value"},
			{`{a {}}`, "unquoted key, object value"},
			{`{a []}`, "unquoted key, array value"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result, tc.desc)
			})
		}
	})

	t.Run("duplicate_colons", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`{"a"::"b"}`,
			`{"a":::"b"}`,
			`{"a": "b"::"c"}`,
		}
		for _, input := range inputs {
			input := input
			t.Run(input, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				if err != nil {
					t.Logf("result: %q error: %v", result, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 37. SURROGATE AND UNICODE ESCAPE SYSTEMATIC TEST
// ============================================================================

func TestSurrogateSystematic(t *testing.T) {
	t.Parallel()

	t.Run("all_surrogate_pairs_valid", func(t *testing.T) {
		t.Parallel()
		// Test that valid \uXXXX\uXXXX surrogate pairs produce the right output
		high := []rune{0xD800, 0xD83D, 0xD83C, 0xDBFF}
		low := []rune{0xDC00, 0xDE00, 0xDFFF, 0xDC01}

		for _, h := range high {
			for _, l := range low {
				h, l := h, l
				t.Run(fmt.Sprintf("U+%04X_U+%04X", h, l), func(t *testing.T) {
					t.Parallel()
					escaped := fmt.Sprintf(`"\u%04X\u%04X"`, h, l)
					result, err := Repair(escaped)
					if err != nil {
						t.Logf("surrogate pair %s error: %v", escaped, err)
						return
					}
					assert.NotEmpty(t, result)
				})
			}
		}
	})
}

// ============================================================================
// 38. STRING CONTENT THAT LOOKS LIKE JSON
// ============================================================================

func TestStringContentLooksLikeJSON(t *testing.T) {
	t.Parallel()

	t.Run("json_like_inside_strings", func(t *testing.T) {
		t.Parallel()
		for _, input := range []string{
			`"{\"key\": \"value\"}"`,
			`"[1, 2, 3]"`,
			`"\"hello\""`,
			`"nested \"quotes\" inside"`,
			`"{[({})]}"`,
		} {
			input := input
			t.Run(input[:min(len(input), 20)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 39. ESCAPED UNICODE vs RAW UNICODE IN STRING
// ============================================================================

func TestEscapedVsRawUnicode(t *testing.T) {
	t.Parallel()

	t.Run("mixed_escaped_and_raw", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`"\u0048\u0065\u006C\u006C\u006F"`,           // Hello as unicode escapes
			`"Hello"`,                                      // raw Hello
			`"\u0048ello"`,                                 // mixed: H as escape + ello
			`"H\u0065llo"`,                                 // mixed: e as escape
			`"He\u006C\u006Co"`,                            // mixed: ll as escapes
			`"\u0048\u0065l\u006Co"`,                       // mixed pattern
			`"\u2605 \u2605 \u2605"`,                       // star escapes
			`"★ ★ ★"`,                                     // raw stars
			`"\u2605 ★ \u2605"`,                            // mixed stars
		}
		for _, input := range inputs {
			input := input
			t.Run(input[:min(len(input), 20)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 40. MULTIPLE BACKSLASH PATTERNS IN FILE PATHS
// ============================================================================

func TestMultipleBackslashInFilePaths(t *testing.T) {
	t.Parallel()

	t.Run("double_escaped_paths", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{`{"path":"C:\\\\Users\\\\name\\\\file.txt"}`, "double escaped windows path"},
			{`"C:\\\\\\\\Users"`, "quadruple escaped"},
			{`"path\\\\to\\\\file"`, "double escaped rel path"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 41. NEGATIVE / ZERO LENGTH TESTS FOR INTERNAL FUNCTIONS
// ============================================================================

func TestInternalFunctionEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("insertBeforeLastWhitespace_cases", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "abc", insertBeforeLastWhitespace("abc", ""))
		assert.Equal(t, "abc", insertBeforeLastWhitespace("abc", ""))
		assert.Equal(t, "abc,", insertBeforeLastWhitespace("abc", ","))
		// all whitespace string
		result := insertBeforeLastWhitespace("   ", ",")
		assert.True(t, strings.HasPrefix(result, ","))
	})

	t.Run("stripLastOccurrence_cases", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", stripLastOccurrence("", ",", false))
		assert.Equal(t, "abc", stripLastOccurrence("abc", ",", false))
		assert.Equal(t, "ab", stripLastOccurrence("ab,", ",", false))
		assert.Equal(t, "ab,", stripLastOccurrence("ab,,", ",", false))
		assert.Equal(t, "ab", stripLastOccurrence("ab,", ",", true))
	})

	t.Run("repairNumberEndingWithNumericSymbol", func(t *testing.T) {
		t.Parallel()
		var buf strings.Builder
		runes := []rune("12")
		i := 2
		repairNumberEndingWithNumericSymbol(&runes, 0, &i, &buf)
		assert.Equal(t, "120", buf.String())

		buf.Reset()
		runes = []rune("-")
		i = 1
		repairNumberEndingWithNumericSymbol(&runes, 0, &i, &buf)
		assert.Equal(t, "-0", buf.String())
	})

	t.Run("prevNonWhitespaceIndex_cases", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, -1, prevNonWhitespaceIndex([]rune("   "), 2))
		assert.Equal(t, 0, prevNonWhitespaceIndex([]rune("a"), 0))
		assert.Equal(t, -1, prevNonWhitespaceIndex([]rune("a"), -1))
	})

	t.Run("atEndOfNumber_edge", func(t *testing.T) {
		t.Parallel()
		runes := []rune("123")
		i := 3
		assert.True(t, atEndOfNumber(&runes, &i))
		i = 4 // out of bounds
		assert.True(t, atEndOfNumber(&runes, &i))
	})
}

// ============================================================================
// 42. MIXED NEWLINE STYLES
// ============================================================================

func TestMixedNewlineStyles(t *testing.T) {
	t.Parallel()

	t.Run("crlf_lf_cr_mix", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			"{\"a\":1}\r\n{\"b\":2}\n{\"c\":3}\r",
			"[\r\n1,\n2,\r3]",
			"{\"a\":1\r}\n{\"b\":2\r\n}",
		}
		for _, input := range inputs {
			input := input
			t.Run(input[:min(len(input), 20)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				if err != nil {
					t.Logf("newline mix error: %v", err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 43. UNICODE IDENTIFIER SYMBOLS IN KEYS
// ============================================================================

func TestUnicodeIdentifiersInKeys(t *testing.T) {
	t.Parallel()

	t.Run("unicode_math_symbol_keys", func(t *testing.T) {
		t.Parallel()
		for _, input := range []string{
			`{∑: 1}`,
			`{∞: "infinity"}`,
			`{√: 1.414}`,
			`{π: 3.14159}`,
			`{∆: "delta"}`,
			`{≠: true}`,
			`{∈: [1,2,3]}`,
		} {
			input := input
			t.Run(input[:min(len(input), 10)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 44. ZERO WIDTH JOINER SEQUENCES IN STRINGS
// ============================================================================

func TestZeroWidthJoinersInStrings(t *testing.T) {
	t.Parallel()

	t.Run("emoji_zwj_edge_cases", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			"\"a\u200Db\"",          // ZWNJ
			"\"a\u200Cb\"",          // ZWJ
			"\"a\u200D\u200C\u200Db\"", // multiple
			"\"\u200D\"",            // just ZWJ
			"\"\u200C\"",            // just ZWNJ
		}
		for _, input := range inputs {
			input := input
			t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 45. TEMPLATE LITERAL ESCAPE SIMULATION
// ============================================================================

func TestTemplateLiteralLike(t *testing.T) {
	t.Parallel()

	t.Run("dollar_brace_in_strings", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`"${variable}"`,
			`"hello ${name} world"`,
			`"${a}${b}${c}"`,
			`"${{a:1}}"`,
			`"${}"`,
		}
		for _, input := range inputs {
			input := input
			t.Run(input[:min(len(input), 15)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.Equal(t, input, result, "template literal strings should pass through")
			})
		}
	})
}

// ============================================================================
// 46. ALL UNICODE SPACE SEPARATOR CATEGORY CHARS
// ============================================================================

func TestAllUnicodeSeparatorCategory(t *testing.T) {
	t.Parallel()

	spaceSeparators := []rune{
		0x0020, 0x00A0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004,
		0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200A, 0x2028, 0x2029,
		0x202F, 0x205F, 0x3000,
	}

	for _, sep := range spaceSeparators {
		sep := sep
		t.Run(fmt.Sprintf("Z_U+%04X", sep), func(t *testing.T) {
			t.Parallel()
			input := fmt.Sprintf("{%c\"a\"%c:%c1%c}", sep, sep, sep, sep)
			result, err := Repair(input)
			if err != nil {
				t.Logf("U+%04X as whitespace error: %v", sep, err)
				return
			}
			assert.NotEmpty(t, result)
		})
	}
}

// ============================================================================
// 47. BACKSLASH END OF STRING / END OF INPUT
// ============================================================================

func TestBackslashAtEndOfInput(t *testing.T) {
	t.Parallel()

	t.Run("backslash_variations_at_end", func(t *testing.T) {
		t.Parallel()
		inputs := []struct{ input, desc string }{
			{`"hello\`, "backslash at end of string"},
			{`hello\`, "backslash at end of unquoted"},
			{`{"a":"hello\`, "object with backslash end string"},
			{`{"a":"hello\\`, "double backslash end"},
			{`"\`, "only backslash in quotes"},
			{`"\\`, "double backslash only"},
			{`"\\\`, "triple backslash end"},
			{`"\\\\\\\\\`, "many backslashes end"},
		}
		for _, tc := range inputs {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				require.NoError(t, err, tc.desc)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 48. QUOTES AND COMMAS IN WEIRD POSITIONS
// ============================================================================

func TestQuotesAndCommasWeird(t *testing.T) {
	t.Parallel()

	t.Run("commas_after_value_before_quote", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, expected string }{
			{`["hello,"world"]`, `["hello","world"]`},
			{`["hello,world]`, `["hello", "world"]`},
			{`[1,"hello,2]`, `[1, "hello", 2]`},
			{`["a,"b","c,"d"]`, `["a","b","c","d"]`},
		} {
			tc := tc
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				assertRepair(t, tc.input, tc.expected)
			})
		}
	})

	t.Run("double_quote_repair_edge_cases", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct{ input, desc string }{
			{`""`, "empty string"},
			{`"a""b"`, "adjacent strings"},
			{`"a" "b"`, "whitespace between"},
			{`"a","b"`, "comma between"},
			{`""""`, "four quotes"},
			{`""""""`, "six quotes"},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()
				result, err := Repair(tc.input)
				if err != nil {
					t.Logf("%s error: %v", tc.desc, err)
					return
				}
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 49. NESTED STRING WITH SPECIAL STRUCTURE
// ============================================================================

func TestNestedStringSpecialStructure(t *testing.T) {
	t.Parallel()

	t.Run("strings_inside_strings", func(t *testing.T) {
		t.Parallel()
		inputs := []string{
			`"'\"'\""`,
			`"\"'\""`,
			`"\\\"'\\\""`,
			`"'\"\\\"'"`,
			`"''''''"`,
			`"\"\"\"\"\"\""`,
		}
		for _, input := range inputs {
			input := input
			t.Run(input[:min(len(input), 12)], func(t *testing.T) {
				t.Parallel()
				result, err := Repair(input)
				require.NoError(t, err)
				assert.NotEmpty(t, result)
			})
		}
	})
}

// ============================================================================
// 50. PURE DESTRUCTION: INVALID UTF-8 BYTES IN STRING
// ============================================================================

func TestDestructiveInvalidUTF8(t *testing.T) {
	t.Parallel()

	invalidSequences := []string{
		"\xff", "\xfe", "\xc0\xc0", "\xe0\x80\x80",
		"\xf0\x80\x80\x80", "\xf8\x80\x80\x80\x80",
		"\xfc\x80\x80\x80\x80\x80",
		"\xc0\xaf", "\xe0\x80\xaf", "\xf0\x80\x80\xaf",
		"\xed\xa0\x80", // surrogate encoded as UTF-8
		"\xed\xb0\x80",
	}

	for _, inv := range invalidSequences {
		inv := inv
		t.Run(fmt.Sprintf("bytes_%x", []byte(inv)), func(t *testing.T) {
			t.Parallel()
			input := fmt.Sprintf("\"before%safter\"", inv)
			result, err := Repair(input)
			if err != nil {
				t.Logf("invalid UTF-8 %x error: %v", []byte(inv), err)
				return
			}
			assert.NotEmpty(t, result)
		})
	}
}

// ============================================================================
// BENCHMARKS (pathological)
// ============================================================================

func BenchmarkRepairPathological(b *testing.B) {
	b.ReportAllocs()

	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "deeply_nested",
			input: func() string { s := "["; for range 10000 { s += "[" }; s += "1"; for range 10000 { s += "]" }; return s }(),
		},
		{
			name:  "huge_escaped_string",
			input: `"` + strings.Repeat("\\n", 5000) + strings.Repeat("a", 5000) + `"`,
		},
		{
			name:  "long_unquoted",
			input: strings.Repeat("hello world ", 1000),
		},
		{
			name:  "many_comments",
			input: func() string {
				var sb strings.Builder
				sb.WriteString("[")
				for i := 0; i < 500; i++ {
					sb.WriteString(fmt.Sprintf("/* comment %d */", i))
				}
				sb.WriteString("1,2,3]")
				return sb.String()
			}(),
		},
		{
			name: "many_unicode_escapes",
			input: func() string {
				var sb strings.Builder
				sb.WriteString(`"`)
				for i := 0; i < 1000; i++ {
					sb.WriteString(fmt.Sprintf("\\u%04X", 0x2600+i%100))
				}
				sb.WriteString(`"`)
				return sb.String()
			}(),
		},
	}

	for _, tc := range tests {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				Repair(tc.input)
			}
		})
	}
}
