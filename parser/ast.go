// Package parser implements a PromQL expression parser.
//
// It produces an AST (Abstract Syntax Tree) from a PromQL query string and
// exposes helpers for traversing the tree.
//
// Supported grammar:
//   - Instant vector selectors:      http_requests_total{job="api",env=~"prod.*"}
//   - Range (matrix) selectors:      http_requests_total[5m]
//   - Aggregation operators:         sum(x) by (label), topk(5, x), quantile(0.95, x)
//   - Function calls:                rate(x[5m]), irate(x[1m]), avg_over_time(x[5m])
//   - Binary operations:             a / b, a + b, a > bool b
//   - Vector matching:               a / on(job) group_left() b
//   - Unary minus:                   -x
//   - Subqueries:                    avg_over_time(rate(x[5m])[1h:])
//   - Parentheses:                   (a + b) * c
//   - Offset modifier:               http_requests_total offset 5m
//   - @ modifier:                    http_requests_total @ 1609746000
package parser

import (
	"fmt"
	"strings"
)

// ----------------------------------------------------------------------------
// Core interface
// ----------------------------------------------------------------------------

// Expr is the common interface for all PromQL AST nodes.
type Expr interface {
	exprNode()
	// String returns a canonical PromQL representation.
	String() string
}

// ----------------------------------------------------------------------------
// Label matchers
// ----------------------------------------------------------------------------

// MatchType describes how a label value must match.
type MatchType int

const (
	MatchEqual     MatchType = iota // =
	MatchNotEqual                   // !=
	MatchRegexp                     // =~
	MatchNotRegexp                  // !~
)

func (m MatchType) String() string {
	switch m {
	case MatchEqual:
		return "="
	case MatchNotEqual:
		return "!="
	case MatchRegexp:
		return "=~"
	case MatchNotRegexp:
		return "!~"
	}
	return "?"
}

// LabelMatcher is a single label matcher inside a vector selector.
type LabelMatcher struct {
	Name  string
	Type  MatchType
	Value string
}

func (m *LabelMatcher) String() string {
	return fmt.Sprintf(`%s%s%q`, m.Name, m.Type, m.Value)
}

// Matches reports whether v satisfies the matcher.
// NOTE: regexp matching is not performed; callers must compile the pattern.
func (m *LabelMatcher) Matches(v string) bool {
	switch m.Type {
	case MatchEqual:
		return v == m.Value
	case MatchNotEqual:
		return v != m.Value
	default:
		return false
	}
}

// ----------------------------------------------------------------------------
// Expression node types
// ----------------------------------------------------------------------------

// VectorSelector represents an instant vector selector.
//
//	http_requests_total{job="api-server", env=~"prod.*"}
type VectorSelector struct {
	Name     string          // metric name (empty for pure label selectors)
	Matchers []*LabelMatcher // additional label matchers
	Offset   int64           // offset in seconds (from `offset 5m`)
	AtTime   *int64          // @ modifier Unix timestamp (nil = not set)
}

func (v *VectorSelector) exprNode() {}
func (v *VectorSelector) String() string {
	var sb strings.Builder
	sb.WriteString(v.Name)
	if len(v.Matchers) > 0 {
		sb.WriteString("{")
		for i, m := range v.Matchers {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(m.String())
		}
		sb.WriteString("}")
	}
	if v.Offset > 0 {
		sb.WriteString(fmt.Sprintf(" offset %s", FormatDuration(v.Offset)))
	}
	return sb.String()
}

// MatrixSelector represents a range vector selector.
//
//	http_requests_total[5m]
type MatrixSelector struct {
	VectorSelector *VectorSelector
	Range          int64 // range in seconds
}

func (m *MatrixSelector) exprNode() {}
func (m *MatrixSelector) String() string {
	return fmt.Sprintf("%s[%s]", m.VectorSelector.String(), FormatDuration(m.Range))
}

// ----------------------------------------------------------------------------
// Aggregation
// ----------------------------------------------------------------------------

// AggregateOp is an aggregation operator keyword.
type AggregateOp string

const (
	OpSum         AggregateOp = "sum"
	OpAvg         AggregateOp = "avg"
	OpCount       AggregateOp = "count"
	OpMin         AggregateOp = "min"
	OpMax         AggregateOp = "max"
	OpStddev      AggregateOp = "stddev"
	OpStdvar      AggregateOp = "stdvar"
	OpTopk        AggregateOp = "topk"
	OpBottomk     AggregateOp = "bottomk"
	OpQuantile    AggregateOp = "quantile"
	OpCountValues AggregateOp = "count_values"
	OpGroup       AggregateOp = "group"
)

// IsAggregateOp reports whether s is a known aggregation operator name.
func IsAggregateOp(s string) bool {
	switch AggregateOp(strings.ToLower(s)) {
	case OpSum, OpAvg, OpCount, OpMin, OpMax, OpStddev, OpStdvar,
		OpTopk, OpBottomk, OpQuantile, OpCountValues, OpGroup:
		return true
	}
	return false
}

// AggregateExpr represents an aggregation expression.
//
//	sum(http_requests_total) by (job)
//	topk(5, http_requests_total)
type AggregateExpr struct {
	Op       AggregateOp
	Expr     Expr     // expression to aggregate over
	Param    Expr     // first parameter for topk/bottomk/quantile/count_values
	Grouping []string // label names for by/without
	Without  bool     // true = without clause; false = by clause
}

func (a *AggregateExpr) exprNode() {}
func (a *AggregateExpr) String() string {
	var sb strings.Builder
	sb.WriteString(string(a.Op))
	if len(a.Grouping) > 0 {
		clause := "by"
		if a.Without {
			clause = "without"
		}
		sb.WriteString(fmt.Sprintf(" %s (%s)", clause, strings.Join(a.Grouping, ", ")))
	}
	sb.WriteString("(")
	if a.Param != nil {
		sb.WriteString(a.Param.String())
		sb.WriteString(", ")
	}
	sb.WriteString(a.Expr.String())
	sb.WriteString(")")
	return sb.String()
}

// ----------------------------------------------------------------------------
// Function calls
// ----------------------------------------------------------------------------

// Call represents a function call.
//
//	rate(http_requests_total[5m])
//	histogram_quantile(0.95, sum(rate(http_request_duration_seconds_bucket[5m])) by (le))
type Call struct {
	Func string
	Args []Expr
}

func (c *Call) exprNode() {}
func (c *Call) String() string {
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		args[i] = a.String()
	}
	return fmt.Sprintf("%s(%s)", c.Func, strings.Join(args, ", "))
}

// ----------------------------------------------------------------------------
// Binary operations
// ----------------------------------------------------------------------------

// BinaryOp is a binary operator.
type BinaryOp string

const (
	BinAdd    BinaryOp = "+"
	BinSub    BinaryOp = "-"
	BinMul    BinaryOp = "*"
	BinDiv    BinaryOp = "/"
	BinMod    BinaryOp = "%"
	BinPow    BinaryOp = "^"
	BinEql    BinaryOp = "=="
	BinNeq    BinaryOp = "!="
	BinGtr    BinaryOp = ">"
	BinLss    BinaryOp = "<"
	BinGte    BinaryOp = ">="
	BinLte    BinaryOp = "<="
	BinAnd    BinaryOp = "and"
	BinOr     BinaryOp = "or"
	BinUnless BinaryOp = "unless"
)

// CardinalityType describes the matching cardinality of a binary operation.
type CardinalityType int

const (
	CardOneToOne  CardinalityType = iota // default
	CardManyToOne                        // group_left
	CardOneToMany                        // group_right
)

// VectorMatching describes how vectors in a binary expression are matched.
type VectorMatching struct {
	Card           CardinalityType
	MatchingLabels []string // labels for on/ignoring
	On             bool     // true = on, false = ignoring
	Include        []string // extra labels for group_left/group_right
}

// BinaryExpr represents a binary operation between two expressions.
//
//	http_requests_total / ignoring(method) http_requests_total_errors
type BinaryExpr struct {
	Op             BinaryOp
	LHS            Expr
	RHS            Expr
	VectorMatching *VectorMatching
	ReturnBool     bool // true when `bool` modifier is present on comparison ops
}

func (b *BinaryExpr) exprNode() {}
func (b *BinaryExpr) String() string {
	op := string(b.Op)
	if b.ReturnBool {
		op += " bool"
	}
	return fmt.Sprintf("%s %s %s", b.LHS.String(), op, b.RHS.String())
}

// ----------------------------------------------------------------------------
// Unary / literals / structural
// ----------------------------------------------------------------------------

// UnaryExpr represents a unary minus.
//
//	-node_cpu_usage
type UnaryExpr struct {
	Expr Expr
}

func (u *UnaryExpr) exprNode() {}
func (u *UnaryExpr) String() string {
	return fmt.Sprintf("-%s", u.Expr.String())
}

// NumberLiteral is a numeric constant.
type NumberLiteral struct {
	Value float64
}

func (n *NumberLiteral) exprNode() {}
func (n *NumberLiteral) String() string {
	return fmt.Sprintf("%g", n.Value)
}

// StringLiteral is a string constant (used in count_values label name, etc.).
type StringLiteral struct {
	Value string
}

func (s *StringLiteral) exprNode() {}
func (s *StringLiteral) String() string {
	return fmt.Sprintf("%q", s.Value)
}

// SubqueryExpr represents a subquery expression.
//
//	avg_over_time(rate(http_requests_total[5m])[1h:])
//	max_over_time(rate(http_requests_total[5m])[1h:1m])
type SubqueryExpr struct {
	Expr   Expr
	Range  int64  // range in seconds
	Step   int64  // step in seconds (0 = default resolution)
	Offset int64  // offset in seconds
	AtTime *int64 // @ modifier timestamp (nil = not set)
}

func (s *SubqueryExpr) exprNode() {}
func (s *SubqueryExpr) String() string {
	step := ""
	if s.Step > 0 {
		step = FormatDuration(s.Step)
	}
	str := fmt.Sprintf("%s[%s:%s]", s.Expr.String(), FormatDuration(s.Range), step)
	if s.Offset > 0 {
		str += fmt.Sprintf(" offset %s", FormatDuration(s.Offset))
	}
	return str
}

// ParenExpr represents a parenthesized expression.
//
//	(a + b) * c
type ParenExpr struct {
	Expr Expr
}

func (p *ParenExpr) exprNode() {}
func (p *ParenExpr) String() string {
	return fmt.Sprintf("(%s)", p.Expr.String())
}

// ----------------------------------------------------------------------------
// Tree walker
// ----------------------------------------------------------------------------

// Visitor is implemented by callers of Walk.
// Visit is called for each node. If it returns nil the node's children
// are not visited.
type Visitor interface {
	Visit(node Expr) Visitor
}

// Walk traverses the AST in depth-first order.
func Walk(v Visitor, node Expr) {
	if v = v.Visit(node); v == nil {
		return
	}
	switch n := node.(type) {
	case *VectorSelector:
		// leaf – no children
	case *MatrixSelector:
		Walk(v, n.VectorSelector)
	case *AggregateExpr:
		if n.Param != nil {
			Walk(v, n.Param)
		}
		Walk(v, n.Expr)
	case *Call:
		for _, arg := range n.Args {
			Walk(v, arg)
		}
	case *BinaryExpr:
		Walk(v, n.LHS)
		Walk(v, n.RHS)
	case *UnaryExpr:
		Walk(v, n.Expr)
	case *NumberLiteral:
		// leaf
	case *StringLiteral:
		// leaf
	case *SubqueryExpr:
		Walk(v, n.Expr)
	case *ParenExpr:
		Walk(v, n.Expr)
	}
}

// Inspect calls f on each node. If f returns true the children are visited.
func Inspect(node Expr, f func(Expr) bool) {
	Walk(inspectorFunc(f), node)
}

type inspectorFunc func(Expr) bool

func (fn inspectorFunc) Visit(node Expr) Visitor {
	if fn(node) {
		return fn
	}
	return nil
}

// ----------------------------------------------------------------------------
// Duration helpers
// ----------------------------------------------------------------------------

// FormatDuration converts seconds into a compact PromQL duration string.
func FormatDuration(secs int64) string {
	if secs <= 0 {
		return "0s"
	}
	if secs%31536000 == 0 {
		return fmt.Sprintf("%dy", secs/31536000)
	}
	if secs%604800 == 0 {
		return fmt.Sprintf("%dw", secs/604800)
	}
	if secs%86400 == 0 {
		return fmt.Sprintf("%dd", secs/86400)
	}
	if secs%3600 == 0 {
		return fmt.Sprintf("%dh", secs/3600)
	}
	if secs%60 == 0 {
		return fmt.Sprintf("%dm", secs/60)
	}
	return fmt.Sprintf("%ds", secs)
}
