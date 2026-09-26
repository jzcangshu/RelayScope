package pricing

import (
	"errors"
	"strconv"
	"strings"
)

var errUnsupportedExpression = errors.New("unsupported billing expression")

// billingExprResult is the linear decomposition of a NewAPI billing
// expression. Coefficient keys are expression variables (p, c, cr, cc, ...)
// and values use the expression's native "USD per 1M tokens" semantics.
type billingExprResult struct {
	coefficients map[string]float64
	fixed        *float64
}

// parseBillingExpression reduces a NewAPI tiered billing expression to the
// linear per-token coefficients of its standard tier, for example
//
//	tier("base", p * 2 + c * 8 + cr * 0.6)
//	len <= 272000 ? tier("standard", p * 2 + c * 12) : tier("long_context", p * 4 + c * 18)
//	(tier("base", p * 4.5 + c * 13.5)) * (hour("UTC") < 4 ? 2 : 1)
//	tier("request", fixed(1))
//
// Conditional branches resolve to the base/standard tier and runtime-only
// multipliers outside the tier call (hour(), param(), request-length
// conditions) are ignored, so the result is the standard per-token price.
// A fixed(N) tier reports a per-request price instead. ok is false when the
// expression cannot be reduced to linear coefficients — legacy model_ratio
// fields do not apply to expression billing, so no price should be shown.
func parseBillingExpression(expression string) (billingExprResult, bool) {
	body := pickTierBody(expression)
	value, err := parseLinearExpression(body)
	if err != nil {
		return billingExprResult{}, false
	}
	if value.fixed != nil {
		if len(value.coefficients) != 0 {
			return billingExprResult{}, false
		}
		return billingExprResult{fixed: value.fixed}, true
	}
	if value.constant != 0 {
		return billingExprResult{}, false
	}
	if value.coefficients["p"] == 0 && value.coefficients["c"] == 0 {
		return billingExprResult{}, false
	}
	for _, coefficient := range value.coefficients {
		if coefficient < 0 {
			return billingExprResult{}, false
		}
	}
	return billingExprResult{coefficients: value.coefficients}, true
}

// pickTierBody returns the expression body of the tier representing the
// standard price: the "base" tier when present, else the "standard" tier,
// else the first tier() call. Bare expressions pass through unchanged.
func pickTierBody(expression string) string {
	type tierCall struct{ name, body string }
	var tiers []tierCall
	for index := 0; index < len(expression); {
		start := indexOfTierCall(expression, index)
		if start < 0 {
			break
		}
		cursor := start + len("tier(")
		name, cursorNext, ok := scanQuotedString(expression, cursor)
		if !ok {
			index = cursor
			continue
		}
		cursor = cursorNext
		for cursor < len(expression) && isSpace(expression[cursor]) {
			cursor++
		}
		if cursor >= len(expression) || expression[cursor] != ',' {
			index = cursor
			continue
		}
		cursor++
		body, cursorNext, ok := scanBalancedParens(expression, cursor)
		if !ok {
			index = cursor
			continue
		}
		tiers = append(tiers, tierCall{name: name, body: body})
		index = cursorNext
	}
	if len(tiers) == 0 {
		return expression
	}
	for _, want := range []string{"base", "standard"} {
		for _, tier := range tiers {
			if strings.TrimSpace(tier.name) == want {
				return tier.body
			}
		}
	}
	return tiers[0].body
}

func indexOfTierCall(expression string, from int) int {
	for {
		index := strings.Index(expression[from:], "tier(")
		if index < 0 {
			return -1
		}
		index += from
		if index == 0 || !isIdentChar(expression[index-1]) {
			return index
		}
		from = index + len("tier(")
	}
}

// scanQuotedString reads a double-quoted string starting at index (which
// must point at the opening quote) and returns its contents plus the index
// just past the closing quote.
func scanQuotedString(expression string, index int) (string, int, bool) {
	if index >= len(expression) || expression[index] != '"' {
		return "", index, false
	}
	end := strings.IndexByte(expression[index+1:], '"')
	if end < 0 {
		return "", index, false
	}
	return expression[index+1 : index+1+end], index + end + 2, true
}

// scanBalancedParens reads from index (after the opening parenthesis of the
// tier arguments, depth still open) and returns the body up to the matching
// closing parenthesis plus the index just past it.
func scanBalancedParens(expression string, index int) (string, int, bool) {
	depth := 1
	for cursor := index; cursor < len(expression); cursor++ {
		switch expression[cursor] {
		case '"':
			_, cursorNext, ok := scanQuotedString(expression, cursor)
			if !ok {
				return "", cursor, false
			}
			cursor = cursorNext - 1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return expression[index:cursor], cursor + 1, true
			}
		}
	}
	return "", index, false
}

// linearValue is the running value of a parsed expression: a constant term,
// per-variable coefficients, or — for fixed(N) — a per-request price.
type linearValue struct {
	constant     float64
	coefficients map[string]float64
	fixed        *float64
}

// parseLinearExpression evaluates the subset of the billing expression
// grammar that can appear in a tier body: numbers, token variables, fixed(),
// and + - * / with parentheses. Anything else (comparisons, unknown
// functions, variable products) fails so callers hide the price instead of
// guessing.
func parseLinearExpression(expression string) (linearValue, error) {
	tokens, err := tokenizeExpression(expression)
	if err != nil {
		return linearValue{}, err
	}
	parser := &exprParser{tokens: tokens}
	value, err := parser.parseExpression()
	if err != nil {
		return linearValue{}, err
	}
	if parser.pos != len(parser.tokens) {
		return linearValue{}, errUnsupportedExpression
	}
	return value, nil
}

func tokenizeExpression(expression string) ([]exprToken, error) {
	var tokens []exprToken
	for index := 0; index < len(expression); {
		char := expression[index]
		switch {
		case isSpace(char):
			index++
		case char >= '0' && char <= '9' || char == '.':
			start := index
			for index < len(expression) && (expression[index] >= '0' && expression[index] <= '9' || expression[index] == '.') {
				index++
			}
			number, err := strconv.ParseFloat(expression[start:index], 64)
			if err != nil {
				return nil, errUnsupportedExpression
			}
			tokens = append(tokens, exprToken{kind: exprNumber, text: expression[start:index], number: number})
		case isIdentChar(char):
			start := index
			for index < len(expression) && isIdentChar(expression[index]) {
				index++
			}
			tokens = append(tokens, exprToken{kind: exprIdent, text: expression[start:index]})
		case char == '"' || char == '\'':
			text, cursorNext, ok := scanQuotedString(expression, index)
			if !ok {
				return nil, errUnsupportedExpression
			}
			tokens = append(tokens, exprToken{kind: exprString, text: text})
			index = cursorNext
		default:
			if strings.ContainsRune("+-*/(),?:<>=!&|", rune(char)) {
				tokens = append(tokens, exprToken{kind: exprOperator, text: string(char)})
				index++
				continue
			}
			return nil, errUnsupportedExpression
		}
	}
	return tokens, nil
}

func isSpace(char byte) bool {
	return char == ' ' || char == '\t' || char == '\r' || char == '\n'
}

func isIdentChar(char byte) bool {
	return char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char >= 0x80
}

const (
	exprNumber = iota
	exprIdent
	exprString
	exprOperator
)

type exprToken struct {
	kind   int
	text   string
	number float64
}

type exprParser struct {
	tokens []exprToken
	pos    int
}

func (parser *exprParser) peek() (exprToken, bool) {
	if parser.pos >= len(parser.tokens) {
		return exprToken{}, false
	}
	return parser.tokens[parser.pos], true
}

func (parser *exprParser) next() (exprToken, bool) {
	token, ok := parser.peek()
	if ok {
		parser.pos++
	}
	return token, ok
}

func (parser *exprParser) acceptOp(text string) bool {
	token, ok := parser.peek()
	if ok && token.kind == exprOperator && token.text == text {
		parser.pos++
		return true
	}
	return false
}

func (parser *exprParser) parseExpression() (linearValue, error) {
	left, err := parser.parseTerm()
	if err != nil {
		return linearValue{}, err
	}
	for {
		token, ok := parser.peek()
		if !ok || token.kind != exprOperator || token.text != "+" && token.text != "-" {
			return left, nil
		}
		parser.next()
		right, err := parser.parseTerm()
		if err != nil {
			return linearValue{}, err
		}
		if left.fixed != nil || right.fixed != nil {
			return linearValue{}, errUnsupportedExpression
		}
		if token.text == "+" {
			left.constant += right.constant
		} else {
			left.constant -= right.constant
		}
		for name, value := range right.coefficients {
			if left.coefficients == nil {
				left.coefficients = make(map[string]float64, len(right.coefficients))
			}
			left.coefficients[name] += value
		}
	}
}

func (parser *exprParser) parseTerm() (linearValue, error) {
	left, err := parser.parseUnary()
	if err != nil {
		return linearValue{}, err
	}
	for {
		token, ok := parser.peek()
		if !ok || token.kind != exprOperator || token.text != "*" && token.text != "/" {
			return left, nil
		}
		parser.next()
		right, err := parser.parseUnary()
		if err != nil {
			return linearValue{}, err
		}
		left, err = combineTerm(left, right, token.text)
		if err != nil {
			return linearValue{}, err
		}
	}
}

func combineTerm(left, right linearValue, operator string) (linearValue, error) {
	if left.fixed != nil || right.fixed != nil {
		return linearValue{}, errUnsupportedExpression
	}
	leftPure := len(left.coefficients) == 0
	rightPure := len(right.coefficients) == 0
	if operator == "/" {
		if !rightPure || right.constant == 0 {
			return linearValue{}, errUnsupportedExpression
		}
		left.constant /= right.constant
		for name := range left.coefficients {
			left.coefficients[name] /= right.constant
		}
		return left, nil
	}
	switch {
	case leftPure && rightPure:
		return linearValue{constant: left.constant * right.constant}, nil
	case leftPure:
		return scaleLinear(right, left.constant), nil
	case rightPure:
		return scaleLinear(left, right.constant), nil
	default:
		return linearValue{}, errUnsupportedExpression
	}
}

func scaleLinear(value linearValue, factor float64) linearValue {
	value.constant *= factor
	for name := range value.coefficients {
		value.coefficients[name] *= factor
	}
	return value
}

func (parser *exprParser) parseUnary() (linearValue, error) {
	token, ok := parser.peek()
	if ok && token.kind == exprOperator && (token.text == "-" || token.text == "+") {
		parser.next()
		value, err := parser.parseUnary()
		if err != nil {
			return linearValue{}, err
		}
		if token.text == "-" {
			if value.fixed != nil {
				return linearValue{}, errUnsupportedExpression
			}
			value.constant = -value.constant
			for name := range value.coefficients {
				value.coefficients[name] = -value.coefficients[name]
			}
		}
		return value, nil
	}
	return parser.parseAtom()
}

func (parser *exprParser) parseAtom() (linearValue, error) {
	token, ok := parser.next()
	if !ok {
		return linearValue{}, errUnsupportedExpression
	}
	switch token.kind {
	case exprNumber:
		return linearValue{constant: token.number}, nil
	case exprIdent:
		if next, has := parser.peek(); has && next.kind == exprOperator && next.text == "(" {
			parser.next()
			return parser.parseFunctionCall(token.text)
		}
		return linearValue{coefficients: map[string]float64{token.text: 1}}, nil
	case exprOperator:
		if token.text != "(" {
			return linearValue{}, errUnsupportedExpression
		}
	default:
		return linearValue{}, errUnsupportedExpression
	}
	value, err := parser.parseExpression()
	if err != nil {
		return linearValue{}, err
	}
	if !parser.acceptOp(")") {
		return linearValue{}, errUnsupportedExpression
	}
	return value, nil
}

func (parser *exprParser) parseFunctionCall(name string) (linearValue, error) {
	if name != "fixed" {
		return linearValue{}, errUnsupportedExpression
	}
	token, ok := parser.next()
	if !ok || token.kind != exprNumber {
		return linearValue{}, errUnsupportedExpression
	}
	value := token.number
	if !parser.acceptOp(")") {
		return linearValue{}, errUnsupportedExpression
	}
	return linearValue{fixed: &value}, nil
}
