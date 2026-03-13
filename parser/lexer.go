package parser

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// ----------------------------------------------------------------------------
// Token types
// ----------------------------------------------------------------------------

// TokenType identifies the kind of a lexical token.
type TokenType int

const (
	// Special
	EOF   TokenType = iota // end of input
	ERROR                  // lex error (Val holds the message)

	// Literals
	NUMBER     // 123, 1.5, .5, 1e10, Inf, NaN
	STRING     // "foo", 'bar', `baz`
	IDENTIFIER // metric_name, label_name, function_name
	DURATION   // 5m, 1h, 30s, 1d, 1w, 1y, 500ms

	// Arithmetic operators
	ADD // +
	SUB // -
	MUL // *
	DIV // /
	MOD // %
	POW // ^

	// Comparison / filter operators
	EQL       // ==
	NEQ       // !=
	LTE       // <=
	LSS       // <
	GTE       // >=
	GTR       // >
	EQL_REGEX // =~
	NEQ_REGEX // !~
	ASSIGN    // =  (used inside label matchers)

	// Punctuation
	LEFT_PAREN    // (
	RIGHT_PAREN   // )
	LEFT_BRACE    // {
	RIGHT_BRACE   // }
	LEFT_BRACKET  // [
	RIGHT_BRACKET // ]
	COMMA         // ,
	COLON         // :
	AT            // @
)

var tokenNames = map[TokenType]string{
	EOF:           "EOF",
	ERROR:         "ERROR",
	NUMBER:        "NUMBER",
	STRING:        "STRING",
	IDENTIFIER:    "IDENTIFIER",
	DURATION:      "DURATION",
	ADD:           "+",
	SUB:           "-",
	MUL:           "*",
	DIV:           "/",
	MOD:           "%",
	POW:           "^",
	EQL:           "==",
	NEQ:           "!=",
	LTE:           "<=",
	LSS:           "<",
	GTE:           ">=",
	GTR:           ">",
	EQL_REGEX:     "=~",
	NEQ_REGEX:     "!~",
	ASSIGN:        "=",
	LEFT_PAREN:    "(",
	RIGHT_PAREN:   ")",
	LEFT_BRACE:    "{",
	RIGHT_BRACE:   "}",
	LEFT_BRACKET:  "[",
	RIGHT_BRACKET: "]",
	COMMA:         ",",
	COLON:         ":",
	AT:            "@",
}

func (t TokenType) String() string {
	if name, ok := tokenNames[t]; ok {
		return name
	}
	return fmt.Sprintf("TokenType(%d)", int(t))
}

// ----------------------------------------------------------------------------
// Token
// ----------------------------------------------------------------------------

// Token is a single lexical unit.
type Token struct {
	Type TokenType
	Val  string
	Pos  int // byte offset in the input string
}

func (t Token) String() string {
	return fmt.Sprintf("Token{%s, %q, @%d}", t.Type, t.Val, t.Pos)
}

// ----------------------------------------------------------------------------
// Public entry point
// ----------------------------------------------------------------------------

// Tokenize tokenizes a PromQL expression and returns all tokens
// (including the terminal EOF token). On lex error the returned error
// is non-nil and the token slice is undefined.
func Tokenize(input string) ([]Token, error) {
	l := &lexer{input: input}
	return l.run()
}

// ----------------------------------------------------------------------------
// Lexer implementation
// ----------------------------------------------------------------------------

type lexer struct {
	input string
	pos   int
}

func (l *lexer) run() ([]Token, error) {
	var tokens []Token
	for {
		l.skipWhitespaceAndComments()
		if l.pos >= len(l.input) {
			break
		}
		tok, err := l.nextToken()
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
	}
	tokens = append(tokens, Token{Type: EOF, Pos: l.pos})
	return tokens, nil
}

func (l *lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			l.pos++
		case ch == '#': // line comment
			for l.pos < len(l.input) && l.input[l.pos] != '\n' {
				l.pos++
			}
		default:
			return
		}
	}
}

func (l *lexer) nextToken() (Token, error) {
	start := l.pos
	ch := l.input[l.pos]

	// String literal
	if ch == '"' || ch == '\'' || ch == '`' {
		return l.scanString(start)
	}

	// Number / duration  (starts with digit or '.')
	if ch >= '0' && ch <= '9' || ch == '.' {
		return l.scanNumberOrDuration(start)
	}

	// Identifier / keyword
	r, _ := utf8.DecodeRuneInString(l.input[l.pos:])
	if r == '_' || unicode.IsLetter(r) {
		return l.scanIdentifier(start)
	}

	// Operators and punctuation
	return l.scanSymbol(start)
}

// scanString scans a quoted string literal.
// Supports "", '' and `` (raw strings). Handles \n, \t, \r, \\ escapes.
func (l *lexer) scanString(start int) (Token, error) {
	quote := l.input[l.pos]
	l.pos++
	var buf []byte

	if quote == '`' {
		// raw string – no escape processing
		for l.pos < len(l.input) {
			if l.input[l.pos] == '`' {
				l.pos++
				return Token{Type: STRING, Val: string(buf), Pos: start}, nil
			}
			buf = append(buf, l.input[l.pos])
			l.pos++
		}
		return Token{}, fmt.Errorf("unterminated raw string at position %d", start)
	}

	for l.pos < len(l.input) {
		ch := l.input[l.pos]
		if ch == '\\' {
			l.pos++
			if l.pos >= len(l.input) {
				return Token{}, fmt.Errorf("unterminated escape sequence at position %d", l.pos)
			}
			switch l.input[l.pos] {
			case 'n':
				buf = append(buf, '\n')
			case 't':
				buf = append(buf, '\t')
			case 'r':
				buf = append(buf, '\r')
			case '\\':
				buf = append(buf, '\\')
			case '"':
				buf = append(buf, '"')
			case '\'':
				buf = append(buf, '\'')
			default:
				buf = append(buf, '\\', l.input[l.pos])
			}
			l.pos++
			continue
		}
		if ch == quote {
			l.pos++
			return Token{Type: STRING, Val: string(buf), Pos: start}, nil
		}
		buf = append(buf, ch)
		l.pos++
	}
	return Token{}, fmt.Errorf("unterminated string literal at position %d", start)
}

// scanNumberOrDuration scans a numeric literal or a duration (e.g. 5m, 1h30m).
func (l *lexer) scanNumberOrDuration(start int) (Token, error) {
	isFloat := false

	// Integer part
	for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
		l.pos++
	}

	// Decimal part
	if l.pos < len(l.input) && l.input[l.pos] == '.' {
		isFloat = true
		l.pos++
		for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
			l.pos++
		}
	}

	// Exponent
	if l.pos < len(l.input) && (l.input[l.pos] == 'e' || l.input[l.pos] == 'E') {
		isFloat = true
		l.pos++
		if l.pos < len(l.input) && (l.input[l.pos] == '+' || l.input[l.pos] == '-') {
			l.pos++
		}
		for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
			l.pos++
		}
	}

	// Duration suffix: only for plain integers (not floats)
	if !isFloat {
		if ok := l.tryConsumeDurationSuffixes(start); ok {
			return Token{Type: DURATION, Val: l.input[start:l.pos], Pos: start}, nil
		}
	}

	return Token{Type: NUMBER, Val: l.input[start:l.pos], Pos: start}, nil
}

// tryConsumeDurationSuffixes tries to consume one or more duration unit suffixes
// (y, w, d, h, m, s, ms) after the current position.
// Returns true if at least one suffix was consumed.
//
// NOTE: ':' is NOT treated as a continuation character here so that durations
// inside range/subquery brackets (e.g. [1h:]) are correctly identified.
func (l *lexer) tryConsumeDurationSuffixes(numStart int) bool {
	_ = numStart
	consumed := false
	for l.pos < len(l.input) {
		// "ms" must be tested before "m"
		if l.pos+1 < len(l.input) && l.input[l.pos] == 'm' && l.input[l.pos+1] == 's' {
			// Ensure next char is not a plain identifier continuation (letters/digits/_)
			if l.pos+2 >= len(l.input) || !isIdentCharNoColon(rune(l.input[l.pos+2])) {
				l.pos += 2
				consumed = true
				// After "ms" there is never another compound suffix
				break
			}
		}
		ch := l.input[l.pos]
		var ok bool
		switch ch {
		case 'y', 'w', 'd', 'h', 'm', 's':
			next := l.pos + 1
			if next >= len(l.input) || !isIdentCharNoColon(rune(l.input[next])) {
				l.pos++
				consumed = true
				ok = true
			}
		}
		if !ok {
			break
		}
		// After a suffix, there may be another number+suffix pair (compound duration)
		// e.g. 1h30m: after '1h' we check for more digits
		if l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
			for l.pos < len(l.input) && l.input[l.pos] >= '0' && l.input[l.pos] <= '9' {
				l.pos++
			}
			// continue the loop to consume the next suffix
		} else {
			break
		}
	}
	return consumed
}

// scanIdentifier scans an identifier or special-case keywords.
// In PromQL, recording-rule metric names may contain ':' (e.g. job:req:rate5m).
func (l *lexer) scanIdentifier(start int) (Token, error) {
	for l.pos < len(l.input) {
		r, size := utf8.DecodeRuneInString(l.input[l.pos:])
		if isIdentChar(r) {
			l.pos += size
		} else {
			break
		}
	}
	val := l.input[start:l.pos]
	return Token{Type: IDENTIFIER, Val: val, Pos: start}, nil
}

// scanSymbol scans a single- or double-character operator / punctuation.
func (l *lexer) scanSymbol(start int) (Token, error) {
	ch := l.input[l.pos]
	l.pos++

	switch ch {
	case '+':
		return Token{Type: ADD, Val: "+", Pos: start}, nil
	case '-':
		return Token{Type: SUB, Val: "-", Pos: start}, nil
	case '*':
		return Token{Type: MUL, Val: "*", Pos: start}, nil
	case '/':
		return Token{Type: DIV, Val: "/", Pos: start}, nil
	case '%':
		return Token{Type: MOD, Val: "%", Pos: start}, nil
	case '^':
		return Token{Type: POW, Val: "^", Pos: start}, nil
	case '(':
		return Token{Type: LEFT_PAREN, Val: "(", Pos: start}, nil
	case ')':
		return Token{Type: RIGHT_PAREN, Val: ")", Pos: start}, nil
	case '{':
		return Token{Type: LEFT_BRACE, Val: "{", Pos: start}, nil
	case '}':
		return Token{Type: RIGHT_BRACE, Val: "}", Pos: start}, nil
	case '[':
		return Token{Type: LEFT_BRACKET, Val: "[", Pos: start}, nil
	case ']':
		return Token{Type: RIGHT_BRACKET, Val: "]", Pos: start}, nil
	case ',':
		return Token{Type: COMMA, Val: ",", Pos: start}, nil
	case ':':
		return Token{Type: COLON, Val: ":", Pos: start}, nil
	case '@':
		return Token{Type: AT, Val: "@", Pos: start}, nil

	case '=':
		if l.pos < len(l.input) {
			switch l.input[l.pos] {
			case '=':
				l.pos++
				return Token{Type: EQL, Val: "==", Pos: start}, nil
			case '~':
				l.pos++
				return Token{Type: EQL_REGEX, Val: "=~", Pos: start}, nil
			}
		}
		return Token{Type: ASSIGN, Val: "=", Pos: start}, nil

	case '!':
		if l.pos < len(l.input) {
			switch l.input[l.pos] {
			case '=':
				l.pos++
				return Token{Type: NEQ, Val: "!=", Pos: start}, nil
			case '~':
				l.pos++
				return Token{Type: NEQ_REGEX, Val: "!~", Pos: start}, nil
			}
		}
		return Token{}, fmt.Errorf("unexpected '!' at position %d (expected '!=' or '!~')", start)

	case '<':
		if l.pos < len(l.input) && l.input[l.pos] == '=' {
			l.pos++
			return Token{Type: LTE, Val: "<=", Pos: start}, nil
		}
		return Token{Type: LSS, Val: "<", Pos: start}, nil

	case '>':
		if l.pos < len(l.input) && l.input[l.pos] == '=' {
			l.pos++
			return Token{Type: GTE, Val: ">=", Pos: start}, nil
		}
		return Token{Type: GTR, Val: ">", Pos: start}, nil
	}

	return Token{}, fmt.Errorf("unexpected character %q at position %d", ch, start)
}

// isIdentChar reports whether r may appear inside an identifier.
// PromQL recording-rule names include ':'.
// Krill extends this to also allow '.' to support metric names like
// node.node_cpu_seconds_total.
func isIdentChar(r rune) bool {
	return r == '_' || r == ':' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// isIdentCharNoColon is like isIdentChar but excludes ':'.
// Used during duration-suffix detection so that [1h:] is parsed correctly.
func isIdentCharNoColon(r rune) bool {
	return r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
