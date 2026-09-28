package engine

import (
	"fmt"
	"strconv"
	"strings"
)

// Eval 对条件表达式做最小化求值，支持：
//   - 比较: == != > >= < <=
//   - 布尔: && || ! 与括号
//   - 字面量: 数字、单/双引号字符串、true/false/null
//   - 变量: 标识符（从变量表取值），支持 a.b 点路径
func Eval(expr string, vars map[string]any) (any, error) {
	p := &parser{tokens: tokenize(expr), vars: vars}
	v, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.tokens) {
		rest := make([]string, 0, len(p.tokens)-p.pos)
		for _, tk := range p.tokens[p.pos:] {
			rest = append(rest, tk.val)
		}
		return nil, fmt.Errorf("表达式存在无法解析的剩余部分: %q", strings.Join(rest, " "))
	}
	return v, nil
}

type tokenKind int

const (
	tkNum tokenKind = iota
	tkStr
	tkIdent
	tkOp
	tkLParen
	tkRParen
)

type token struct {
	kind tokenKind
	val  string
}

func tokenize(s string) []token {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '(':
			toks = append(toks, token{tkLParen, "("})
			i++
		case c == ')':
			toks = append(toks, token{tkRParen, ")"})
			i++
		case c == '\'' || c == '"':
			quote := c
			j := i + 1
			for j < len(s) && s[j] != quote {
				j++
			}
			toks = append(toks, token{tkStr, s[i+1 : j]})
			i = j + 1
		case c >= '0' && c <= '9' || (c == '-' && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' && !numOrIdentAdjacent(toks)):
			j := i + 1
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			toks = append(toks, token{tkNum, s[i:j]})
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			toks = append(toks, token{tkIdent, s[i:j]})
			i = j
		default:
			// 多字符操作符优先
			three := ""
			if i+3 <= len(s) {
				three = s[i : i+3]
			}
			two := ""
			if i+2 <= len(s) {
				two = s[i : i+2]
			}
			switch {
			case two == "==" || two == "!=" || two == ">=" || two == "<=" || two == "&&" || two == "||":
				toks = append(toks, token{tkOp, two})
				i += 2
			case c == '!' || c == '>' || c == '<':
				toks = append(toks, token{tkOp, string(c)})
				i++
			case three == "...":
				toks = append(toks, token{tkOp, three})
				i += 3
			default:
				// 未知字符原样保留，解析阶段会报错
				toks = append(toks, token{tkOp, string(c)})
				i++
			}
		}
	}
	return toks
}

func numOrIdentAdjacent(toks []token) bool {
	return len(toks) > 0 && (toks[len(toks)-1].kind == tkNum || toks[len(toks)-1].kind == tkIdent)
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c > 127
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9' || c == '.'
}

type parser struct {
	tokens []token
	pos    int
	vars   map[string]any
}

func (p *parser) peek() *token {
	if p.pos < len(p.tokens) {
		return &p.tokens[p.pos]
	}
	return nil
}

func (p *parser) parseOr() (any, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for t := p.peek(); t != nil && t.kind == tkOp && t.val == "||"; t = p.peek() {
		p.pos++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = truthy(left) || truthy(right)
	}
	return left, nil
}

func (p *parser) parseAnd() (any, error) {
	left, err := p.parseCmp()
	if err != nil {
		return nil, err
	}
	for t := p.peek(); t != nil && t.kind == tkOp && t.val == "&&"; t = p.peek() {
		p.pos++
		right, err := p.parseCmp()
		if err != nil {
			return nil, err
		}
		left = truthy(left) && truthy(right)
	}
	return left, nil
}

func (p *parser) parseCmp() (any, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t != nil && t.kind == tkOp {
		switch t.val {
		case "==", "!=", ">", ">=", "<", "<=":
			p.pos++
			right, err := p.parseUnary()
			if err != nil {
				return nil, err
			}
			return compare(t.val, left, right)
		}
	}
	return left, nil
}

func (p *parser) parseUnary() (any, error) {
	t := p.peek()
	if t != nil && t.kind == tkOp && t.val == "!" {
		p.pos++
		v, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return !truthy(v), nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (any, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("表达式意外结束")
	}
	switch t.kind {
	case tkLParen:
		p.pos++
		v, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if q := p.peek(); q == nil || q.kind != tkRParen {
			return nil, fmt.Errorf("缺少右括号")
		}
		p.pos++
		return v, nil
	case tkNum:
		p.pos++
		f, err := strconv.ParseFloat(t.val, 64)
		if err != nil {
			return nil, err
		}
		return f, nil
	case tkStr:
		p.pos++
		return t.val, nil
	case tkIdent:
		p.pos++
		switch t.val {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		return lookup(p.vars, t.val), nil
	default:
		return nil, fmt.Errorf("无法解析的记号 %q", t.val)
	}
}

func lookup(vars map[string]any, path string) any {
	parts := strings.Split(path, ".")
	var cur any = vars
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[part]
	}
	return cur
}

func compare(op string, a, b any) (bool, error) {
	if op == "==" || op == "!=" {
		eq := equalish(a, b)
		if op == "!=" {
			return !eq, nil
		}
		return eq, nil
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if !aok || !bok {
		return false, fmt.Errorf("只能对数字使用 %q 比较", op)
	}
	switch op {
	case ">":
		return af > bf, nil
	case ">=":
		return af >= bf, nil
	case "<":
		return af < bf, nil
	case "<=":
		return af <= bf, nil
	}
	return false, fmt.Errorf("未知比较符 %q", op)
}

func equalish(a, b any) bool {
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return as == bs
		}
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		return af == bf
	}
	return a == b
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}
