package parser

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ----------------------------------------------------------------------------
// Public API
// ----------------------------------------------------------------------------

// Parse parses a PromQL expression string and returns the root AST node.
// Returns an error if the expression is syntactically invalid.
//
// Examples:
//
//	Parse("http_requests_total")
//	Parse(`sum(rate(http_requests_total[5m])) by (job)`)
//	Parse(`node_memory_MemFree_bytes / node_memory_MemTotal_bytes * 100`)
func Parse(input string) (Expr, error) {
	tokens, err := Tokenize(input)
	if err != nil {
		return nil, fmt.Errorf("lex error: %w", err)
	}
	p := &parser{tokens: tokens}
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if !p.peek(EOF) {
		cur := p.current()
		return nil, fmt.Errorf("unexpected token %q at position %d, expected end of expression",
			cur.Val, cur.Pos)
	}
	return expr, nil
}

// ParseDuration converts a PromQL duration string to seconds.
// Handles compound durations like "1h30m".
// Sub-second units (ms) are rounded to 0.
//
// Examples: "5m"→300, "1h"→3600, "1d"→86400, "1h30m"→5400
func ParseDuration(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}
	var total int64
	remaining := s
	for remaining != "" {
		// Scan digits
		i := 0
		for i < len(remaining) && remaining[i] >= '0' && remaining[i] <= '9' {
			i++
		}
		if i == 0 {
			return 0, fmt.Errorf("invalid duration %q: expected digit at %q", s, remaining)
		}
		num, err := strconv.ParseInt(remaining[:i], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		remaining = remaining[i:]

		// Scan unit
		switch {
		case strings.HasPrefix(remaining, "ms"):
			// milliseconds — round to 0 seconds
			remaining = remaining[2:]
		case strings.HasPrefix(remaining, "s"):
			total += num
			remaining = remaining[1:]
		case strings.HasPrefix(remaining, "m"):
			total += num * 60
			remaining = remaining[1:]
		case strings.HasPrefix(remaining, "h"):
			total += num * 3600
			remaining = remaining[1:]
		case strings.HasPrefix(remaining, "d"):
			total += num * 86400
			remaining = remaining[1:]
		case strings.HasPrefix(remaining, "w"):
			total += num * 604800
			remaining = remaining[1:]
		case strings.HasPrefix(remaining, "y"):
			total += num * 31536000
			remaining = remaining[1:]
		default:
			unit := remaining
			if len(unit) > 3 {
				unit = unit[:3]
			}
			return 0, fmt.Errorf("unknown duration unit %q in %q", unit, s)
		}
	}
	return total, nil
}

// ----------------------------------------------------------------------------
// Parser
// ----------------------------------------------------------------------------

type parser struct {
	tokens []Token
	pos    int
}

// current returns the token at the current position.
func (p *parser) current() Token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return Token{Type: EOF}
}

// peek returns true when the current token has the given type.
func (p *parser) peek(typ TokenType) bool {
	return p.current().Type == typ
}

// peekIdent returns true when the current token is an IDENTIFIER with the
// given value (case-insensitive).
func (p *parser) peekIdent(val string) bool {
	tok := p.current()
	return tok.Type == IDENTIFIER && strings.EqualFold(tok.Val, val)
}

// consume advances past the current token and returns it.
func (p *parser) consume() Token {
	tok := p.current()
	p.pos++
	return tok
}

// expect consumes and returns the current token if it matches typ; otherwise
// returns an error.
func (p *parser) expect(typ TokenType) (Token, error) {
	tok := p.current()
	if tok.Type != typ {
		return Token{}, fmt.Errorf("expected %s but got %s (%q) at position %d",
			typ, tok.Type, tok.Val, tok.Pos)
	}
	p.pos++
	return tok, nil
}

// ----------------------------------------------------------------------------
// Expression grammar (lowest to highest precedence)
//
//   expr       = or
//   or         = andUnless { "or" [vectorMatching] andUnless }
//   andUnless  = comparison { ("and"|"unless") [vectorMatching] comparison }
//   comparison = addSub { cmpOp ["bool"] [vectorMatching] addSub }
//   addSub     = mulDiv { ("+"|"-") [vectorMatching] mulDiv }
//   mulDiv     = pow { ("*"|"/"|"%") [vectorMatching] pow }
//   pow        = unary [ "^" [vectorMatching] pow ]       (right-assoc)
//   unary      = ["-"|"+"] postfix
//   postfix    = primary { "[" duration [":"[duration]] "]" } { "offset" duration } { "@" timestamp }
//   primary    = NUMBER | STRING | "(" expr ")" | "{" labelMatchers "}"
//              | IDENTIFIER ( "(" args ")" | "{" labelMatchers "}" | ε )
//              | aggregateExpr
// ----------------------------------------------------------------------------

func (p *parser) parseExpr() (Expr, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (Expr, error) {
	lhs, err := p.parseAndUnless()
	if err != nil {
		return nil, err
	}
	for p.peekIdent("or") {
		p.consume()
		vm, err := p.parseVectorMatching()
		if err != nil {
			return nil, err
		}
		rhs, err := p.parseAndUnless()
		if err != nil {
			return nil, err
		}
		lhs = &BinaryExpr{Op: BinOr, LHS: lhs, RHS: rhs, VectorMatching: vm}
	}
	return lhs, nil
}

func (p *parser) parseAndUnless() (Expr, error) {
	lhs, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.peekIdent("and") || p.peekIdent("unless") {
		opTok := p.consume()
		op := BinAnd
		if strings.EqualFold(opTok.Val, "unless") {
			op = BinUnless
		}
		vm, err := p.parseVectorMatching()
		if err != nil {
			return nil, err
		}
		rhs, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		lhs = &BinaryExpr{Op: op, LHS: lhs, RHS: rhs, VectorMatching: vm}
	}
	return lhs, nil
}

func (p *parser) parseComparison() (Expr, error) {
	lhs, err := p.parseAddSub()
	if err != nil {
		return nil, err
	}
	for {
		var op BinaryOp
		switch p.current().Type {
		case EQL:
			op = BinEql
		case NEQ:
			op = BinNeq
		case GTR:
			op = BinGtr
		case LSS:
			op = BinLss
		case GTE:
			op = BinGte
		case LTE:
			op = BinLte
		default:
			return lhs, nil
		}
		p.consume()

		// Optional `bool` modifier
		retBool := false
		if p.peekIdent("bool") {
			p.consume()
			retBool = true
		}

		vm, err := p.parseVectorMatching()
		if err != nil {
			return nil, err
		}
		rhs, err := p.parseAddSub()
		if err != nil {
			return nil, err
		}
		lhs = &BinaryExpr{Op: op, LHS: lhs, RHS: rhs, VectorMatching: vm, ReturnBool: retBool}
	}
}

func (p *parser) parseAddSub() (Expr, error) {
	lhs, err := p.parseMulDiv()
	if err != nil {
		return nil, err
	}
	for p.peek(ADD) || p.peek(SUB) {
		op := BinAdd
		if p.consume().Type == SUB {
			op = BinSub
		}
		vm, err := p.parseVectorMatching()
		if err != nil {
			return nil, err
		}
		rhs, err := p.parseMulDiv()
		if err != nil {
			return nil, err
		}
		lhs = &BinaryExpr{Op: op, LHS: lhs, RHS: rhs, VectorMatching: vm}
	}
	return lhs, nil
}

func (p *parser) parseMulDiv() (Expr, error) {
	lhs, err := p.parsePow()
	if err != nil {
		return nil, err
	}
	for p.peek(MUL) || p.peek(DIV) || p.peek(MOD) {
		var op BinaryOp
		switch p.consume().Type {
		case MUL:
			op = BinMul
		case DIV:
			op = BinDiv
		default:
			op = BinMod
		}
		vm, err := p.parseVectorMatching()
		if err != nil {
			return nil, err
		}
		rhs, err := p.parsePow()
		if err != nil {
			return nil, err
		}
		lhs = &BinaryExpr{Op: op, LHS: lhs, RHS: rhs, VectorMatching: vm}
	}
	return lhs, nil
}

// parsePow is right-associative.
func (p *parser) parsePow() (Expr, error) {
	lhs, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	if p.peek(POW) {
		p.consume()
		vm, err := p.parseVectorMatching()
		if err != nil {
			return nil, err
		}
		// Recursive call for right-associativity
		rhs, err := p.parsePow()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: BinPow, LHS: lhs, RHS: rhs, VectorMatching: vm}, nil
	}
	return lhs, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.peek(SUB) {
		p.consume()
		expr, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		// Fold constant negation
		if num, ok := expr.(*NumberLiteral); ok {
			return &NumberLiteral{Value: -num.Value}, nil
		}
		return &UnaryExpr{Expr: expr}, nil
	}
	if p.peek(ADD) {
		p.consume() // unary + is a no-op
	}
	return p.parsePostfix()
}

// parsePostfix handles postfix operators: [range], offset, @.
func (p *parser) parsePostfix() (Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	// Range selector '[' duration [':' [duration]] ']'
	if p.peek(LEFT_BRACKET) {
		p.consume()
		rangeSecs, err := p.parseDurationToken()
		if err != nil {
			return nil, fmt.Errorf("expected duration in range selector: %w", err)
		}

		if p.peek(COLON) {
			// Subquery: expr[range:step]
			p.consume()
			var step int64
			if p.peek(DURATION) {
				step, err = p.parseDurationToken()
				if err != nil {
					return nil, err
				}
			}
			if _, err := p.expect(RIGHT_BRACKET); err != nil {
				return nil, err
			}
			expr = &SubqueryExpr{Expr: expr, Range: rangeSecs, Step: step}
		} else {
			if _, err := p.expect(RIGHT_BRACKET); err != nil {
				return nil, err
			}
			vs, ok := expr.(*VectorSelector)
			if !ok {
				return nil, fmt.Errorf("range selector applied to non-vector expression")
			}
			expr = &MatrixSelector{VectorSelector: vs, Range: rangeSecs}
		}
	}

	// Offset modifier
	if p.peekIdent("offset") {
		p.consume()
		offset, err := p.parseDurationToken()
		if err != nil {
			return nil, fmt.Errorf("expected duration after 'offset': %w", err)
		}
		switch e := expr.(type) {
		case *VectorSelector:
			e.Offset = offset
		case *MatrixSelector:
			e.VectorSelector.Offset = offset
		case *SubqueryExpr:
			e.Offset = offset
		default:
			return nil, fmt.Errorf("'offset' can only be applied to selectors and subqueries")
		}
	}

	// @ modifier
	if p.peek(AT) {
		p.consume()
		ts, err := p.parseAtTimestamp()
		if err != nil {
			return nil, err
		}
		switch e := expr.(type) {
		case *VectorSelector:
			e.AtTime = &ts
		case *MatrixSelector:
			e.VectorSelector.AtTime = &ts
		case *SubqueryExpr:
			e.AtTime = &ts
		default:
			return nil, fmt.Errorf("'@' modifier can only be applied to selectors and subqueries")
		}
	}

	return expr, nil
}

// parseAtTimestamp parses the value after the '@' modifier.
// Accepts a Unix timestamp number, or start()/end() placeholders.
func (p *parser) parseAtTimestamp() (int64, error) {
	if p.peekIdent("start") || p.peekIdent("end") {
		p.consume()
		if _, err := p.expect(LEFT_PAREN); err != nil {
			return 0, err
		}
		if _, err := p.expect(RIGHT_PAREN); err != nil {
			return 0, err
		}
		return 0, nil // placeholder; resolved at query evaluation time
	}
	tok, err := p.expect(NUMBER)
	if err != nil {
		return 0, fmt.Errorf("expected Unix timestamp after '@': %w", err)
	}
	f, err := parseFloatVal(tok.Val)
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp %q: %w", tok.Val, err)
	}
	return int64(f), nil
}

// parsePrimary parses atomic expressions: literals, parentheses, selectors,
// function calls, and aggregation expressions.
func (p *parser) parsePrimary() (Expr, error) {
	tok := p.current()

	switch tok.Type {

	case NUMBER:
		p.consume()
		v, err := parseFloatVal(tok.Val)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q: %w", tok.Val, err)
		}
		return &NumberLiteral{Value: v}, nil

	case STRING:
		p.consume()
		return &StringLiteral{Value: tok.Val}, nil

	case LEFT_PAREN:
		p.consume()
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(RIGHT_PAREN); err != nil {
			return nil, err
		}
		return &ParenExpr{Expr: expr}, nil

	case LEFT_BRACE:
		// Pure label selector: {job="api-server"}
		return p.parseLabelOnlySelector()

	case IDENTIFIER:
		name := tok.Val
		// Aggregation operators are parsed specially
		if IsAggregateOp(strings.ToLower(name)) {
			return p.parseAggregateExpr()
		}
		// Consume the identifier and decide what follows
		p.consume()

		if p.peek(LEFT_BRACE) {
			// metric{label="val"}
			matchers, err := p.parseLabelMatchers()
			if err != nil {
				return nil, err
			}
			return &VectorSelector{Name: name, Matchers: matchers}, nil
		}
		if p.peek(LEFT_PAREN) {
			// function call
			return p.parseFunctionCall(name)
		}
		// Plain metric selector
		return &VectorSelector{Name: name}, nil

	default:
		return nil, fmt.Errorf("unexpected token %s (%q) at position %d",
			tok.Type, tok.Val, tok.Pos)
	}
}

// parseLabelOnlySelector parses {label="val", ...} selectors.
func (p *parser) parseLabelOnlySelector() (Expr, error) {
	matchers, err := p.parseLabelMatchers()
	if err != nil {
		return nil, err
	}
	// Extract __name__ if present
	name := ""
	var remaining []*LabelMatcher
	for _, m := range matchers {
		if m.Name == "__name__" && m.Type == MatchEqual {
			name = m.Value
		} else {
			remaining = append(remaining, m)
		}
	}
	return &VectorSelector{Name: name, Matchers: remaining}, nil
}

// parseAggregateExpr parses an aggregation expression.
// Both forms are supported:
//
//	sum by (label) (expr)
//	sum(expr) by (label)
func (p *parser) parseAggregateExpr() (Expr, error) {
	opTok := p.consume()
	op := AggregateOp(strings.ToLower(opTok.Val))
	agg := &AggregateExpr{Op: op}

	// Optional by/without BEFORE the argument list
	if p.peekIdent("by") || p.peekIdent("without") {
		agg.Without = p.peekIdent("without")
		p.consume()
		grouping, err := p.parseGroupingLabels()
		if err != nil {
			return nil, err
		}
		agg.Grouping = grouping
	}

	if _, err := p.expect(LEFT_PAREN); err != nil {
		return nil, err
	}

	// Some operators take a leading parameter before the main expression
	switch op {
	case OpTopk, OpBottomk, OpQuantile:
		param, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		agg.Param = param
		if _, err := p.expect(COMMA); err != nil {
			return nil, fmt.Errorf("expected ',' after %s parameter: %w", op, err)
		}
	case OpCountValues:
		param, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		agg.Param = param
		if _, err := p.expect(COMMA); err != nil {
			return nil, fmt.Errorf("expected ',' after count_values label parameter: %w", err)
		}
	}

	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	agg.Expr = expr

	if _, err := p.expect(RIGHT_PAREN); err != nil {
		return nil, err
	}

	// Optional by/without AFTER the argument list (if not already set)
	if agg.Grouping == nil {
		if p.peekIdent("by") || p.peekIdent("without") {
			agg.Without = p.peekIdent("without")
			p.consume()
			grouping, err := p.parseGroupingLabels()
			if err != nil {
				return nil, err
			}
			agg.Grouping = grouping
		}
	}

	return agg, nil
}

// parseFunctionCall parses func(arg1, arg2, ...) after the function name has
// been consumed by the caller.
func (p *parser) parseFunctionCall(name string) (Expr, error) {
	if _, err := p.expect(LEFT_PAREN); err != nil {
		return nil, err
	}
	var args []Expr
	for !p.peek(RIGHT_PAREN) && !p.peek(EOF) {
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if !p.peek(COMMA) {
			break
		}
		p.consume()
	}
	if _, err := p.expect(RIGHT_PAREN); err != nil {
		return nil, err
	}
	return &Call{Func: name, Args: args}, nil
}

// parseLabelMatchers parses a label matcher list: { matcher, matcher, ... }
func (p *parser) parseLabelMatchers() ([]*LabelMatcher, error) {
	if _, err := p.expect(LEFT_BRACE); err != nil {
		return nil, err
	}
	var matchers []*LabelMatcher
	for !p.peek(RIGHT_BRACE) && !p.peek(EOF) {
		m, err := p.parseSingleMatcher()
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, m)
		if !p.peek(COMMA) {
			break
		}
		p.consume()
	}
	if _, err := p.expect(RIGHT_BRACE); err != nil {
		return nil, err
	}
	return matchers, nil
}

// parseSingleMatcher parses one label matcher: label op "value"
func (p *parser) parseSingleMatcher() (*LabelMatcher, error) {
	nameTok, err := p.expect(IDENTIFIER)
	if err != nil {
		return nil, fmt.Errorf("expected label name in matcher: %w", err)
	}
	var mt MatchType
	switch p.current().Type {
	case ASSIGN:
		mt = MatchEqual
	case EQL:
		mt = MatchEqual // tolerate == inside label matchers
	case NEQ:
		mt = MatchNotEqual
	case EQL_REGEX:
		mt = MatchRegexp
	case NEQ_REGEX:
		mt = MatchNotRegexp
	default:
		return nil, fmt.Errorf("expected matcher operator (=, !=, =~, !~) but got %s at position %d",
			p.current().Type, p.current().Pos)
	}
	p.consume()

	// Accept both quoted strings and unquoted identifiers as label values.
	// e.g. {device="lo"} and {device=lo} are both valid in Krill.
	var val string
	switch p.current().Type {
	case STRING:
		val = p.consume().Val
	case IDENTIFIER:
		val = p.consume().Val
	default:
		return nil, fmt.Errorf("expected string value in label matcher but got %s at position %d",
			p.current().Type, p.current().Pos)
	}
	return &LabelMatcher{Name: nameTok.Val, Type: mt, Value: val}, nil
}

// parseGroupingLabels parses ( label1, label2, ... )
func (p *parser) parseGroupingLabels() ([]string, error) {
	if _, err := p.expect(LEFT_PAREN); err != nil {
		return nil, err
	}
	var labels []string
	for !p.peek(RIGHT_PAREN) && !p.peek(EOF) {
		tok, err := p.expect(IDENTIFIER)
		if err != nil {
			return nil, fmt.Errorf("expected label name in grouping clause: %w", err)
		}
		labels = append(labels, tok.Val)
		if !p.peek(COMMA) {
			break
		}
		p.consume()
	}
	if _, err := p.expect(RIGHT_PAREN); err != nil {
		return nil, err
	}
	return labels, nil
}

// parseVectorMatching optionally parses on/ignoring and group_left/group_right.
// It returns a non-nil VectorMatching only when such a clause is present.
func (p *parser) parseVectorMatching() (*VectorMatching, error) {
	if !p.peekIdent("on") && !p.peekIdent("ignoring") {
		return nil, nil
	}
	vm := &VectorMatching{Card: CardOneToOne}
	vm.On = p.peekIdent("on")
	p.consume()

	labels, err := p.parseGroupingLabels()
	if err != nil {
		return nil, err
	}
	vm.MatchingLabels = labels

	if p.peekIdent("group_left") {
		p.consume()
		vm.Card = CardManyToOne
		if p.peek(LEFT_PAREN) {
			inc, err := p.parseGroupingLabels()
			if err != nil {
				return nil, err
			}
			vm.Include = inc
		}
	} else if p.peekIdent("group_right") {
		p.consume()
		vm.Card = CardOneToMany
		if p.peek(LEFT_PAREN) {
			inc, err := p.parseGroupingLabels()
			if err != nil {
				return nil, err
			}
			vm.Include = inc
		}
	}
	return vm, nil
}

// parseDurationToken consumes one or more consecutive DURATION tokens and sums
// them. This handles compound durations like 1h30m which the lexer emits as
// two separate DURATION tokens.
func (p *parser) parseDurationToken() (int64, error) {
	if !p.peek(DURATION) {
		return 0, fmt.Errorf("expected duration but got %s (%q) at position %d",
			p.current().Type, p.current().Val, p.current().Pos)
	}
	var total int64
	for p.peek(DURATION) {
		tok := p.consume()
		d, err := ParseDuration(tok.Val)
		if err != nil {
			return 0, err
		}
		total += d
	}
	return total, nil
}

// parseFloatVal parses a float64, handling the special values Inf and NaN.
func parseFloatVal(s string) (float64, error) {
	lower := strings.ToLower(s)
	switch lower {
	case "inf", "+inf":
		return math.Inf(1), nil
	case "-inf":
		return math.Inf(-1), nil
	case "nan":
		return math.NaN(), nil
	}
	return strconv.ParseFloat(s, 64)
}
