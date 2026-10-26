package transpile

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/printer"
)

// sa_jsx.go -- JSX element lowering to jsval descriptors (JEV
// jsx_jsval_descriptor, confidence 1.0, probability 1.0; jsx_scope
// simple_only, confidence 0.98, probability 0.99).
//
// A JSX element value is a jsval object (kind 4) built by the sa_std
// helper @ts_jsx_element (sa_std/ts_jsx.sa): payload is a 24-byte element
// struct { tag str handle, props btree_map handle (jsval values),
// children jsval-array box handle }. Tag names are strings only: intrinsic
// tags use the tag text, component tags use the component name as a
// string -- component identity is NOT a function reference, and no DOM
// semantics are claimed. This is a structural lowering for compiler
// testing, not a runnable React app.
//
// Scope (simple_only): intrinsic tags, fragments (tag ""), self-closing
// tags, string / expression attributes, text / expression children,
// nested elements. Refused loudly: spread attributes, member tags
// (Foo.Bar), namespaced tags, namespaced attribute names, and any child
// or attribute value that is not lowerable as jsval. Child expressions
// are always evaluated and boxed -- never dropped. JSX text that is
// whitespace-only is skipped (React whitespace-line rule); other JSX
// text is trimmed (same approximation, documented).
//
// Ownership: tag / props / children handles are transferred into the
// descriptor (saConsumeOwn after the helper call, same discipline as
// object-literal fields stored into an inst). Attribute and child jsval
// temps are stored into the map / array and consumed the same way, so
// scope exit does not release a handle the descriptor still references.
// JSX is only accepted in jsval positions (saEvalCallArg jsval branch);
// i32 / str / arr positions refuse it through their existing gates.

// saJsxTagName returns the tag string for an opening / self-closing
// element, or a refusal message for member / namespaced tags.
func saJsxTagName(tag *ast.Node) (string, string) {
	if tag == nil {
		return "", "JSX element has no tag"
	}
	if tag.Kind != ast.KindIdentifier {
		return "", "JSX member / namespaced tags are not lowerable yet"
	}
	return tag.Text(), ""
}

// saJsxAttributes builds a btree_map of jsval attribute values from a
// JsxAttributes node. It returns the map handle, or "" + a message.
func saJsxAttributes(w printer.EmitTextWriter, attrs *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	scope.addImport("sa_std/btree_map.sa")
	m := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @sa_btree_map_new()\n", m))
	saOwnTemp(scope, m)
	if attrs == nil {
		return m, ""
	}
	ja := attrs.AsJsxAttributes()
	if ja == nil || ja.Properties == nil {
		return m, ""
	}
	for _, p := range ja.Properties.Nodes {
		if p == nil {
			continue
		}
		if p.Kind == ast.KindJsxSpreadAttribute {
			return "", "JSX spread attributes are not lowerable yet"
		}
		if p.Kind != ast.KindJsxAttribute {
			return "", "JSX attribute is not lowerable"
		}
		at := p.AsJsxAttribute()
		if at == nil || at.Name() == nil || at.Name().Kind != ast.KindIdentifier {
			return "", "JSX namespaced attribute names are not lowerable yet"
		}
		name := at.Name().Text()
		var v string
		if at.Initializer == nil {
			// Bare attribute: JSX bare attribute means true.
			v = saBoxJsValStd(w, scope, "i32", 3, "1", nextTemp)
			saOwnTemp(scope, v)
		} else {
			init := at.Initializer.AsNode()
			if init == nil {
				return "", "JSX attribute has no value"
			}
			if init.Kind == ast.KindStringLiteral {
				sv := saLowerStringLiteral(w, init.Text(), scope, nextTemp)
				v = saBoxJsValStd(w, scope, "ptr", 1, sv, nextTemp)
				saOwnTemp(scope, v)
				saConsumeOwn(scope, sv)
			} else if init.Kind == ast.KindJsxExpression {
				je := init.AsJsxExpression()
				if je == nil || je.Expression == nil {
					return "", "JSX attribute expression is empty"
				}
				h, msg := saEvalJsValOperand(w, je.Expression.AsNode(), scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				v = h
			} else if init.Kind == ast.KindJsxElement || init.Kind == ast.KindJsxSelfClosingElement || init.Kind == ast.KindJsxFragment {
				h, msg := saLowerJsxElement(w, init, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				v = h
			} else {
				return "", "JSX attribute value is not lowerable"
			}
		}
		kh := saLowerStringLiteral(w, name, scope, nextTemp)
		w.Write(fmt.Sprintf("  call @sa_btree_map_insert(&%s, &%s, %s)\n", m, kh, v))
		saConsumeOwn(scope, kh)
		saConsumeOwn(scope, v)
	}
	return m, ""
}

// saJsxChildren builds a jsval-array box of the child jsvals for a
// JsxChildList, or "" + a message.
func saJsxChildren(w printer.EmitTextWriter, children *ast.NodeList, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	var hs []string
	if children != nil {
		for _, c := range children.Nodes {
			if c == nil {
				continue
			}
			switch c.Kind {
			case ast.KindJsxText, ast.KindJsxTextAllWhiteSpaces:
				jt := c.AsJsxText()
				if jt == nil || jt.ContainsOnlyTriviaWhiteSpaces {
					continue
				}
				txt := strings.TrimSpace(jt.Text)
				if txt == "" {
					continue
				}
				sv := saLowerStringLiteral(w, txt, scope, nextTemp)
				h := saBoxJsValStd(w, scope, "ptr", 1, sv, nextTemp)
				saOwnTemp(scope, h)
				saConsumeOwn(scope, sv)
				hs = append(hs, h)
			case ast.KindJsxExpression:
				je := c.AsJsxExpression()
				if je == nil || je.Expression == nil {
					continue
				}
				h, msg := saEvalJsValOperand(w, je.Expression.AsNode(), scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				hs = append(hs, h)
			case ast.KindJsxElement, ast.KindJsxSelfClosingElement, ast.KindJsxFragment:
				h, msg := saLowerJsxElement(w, c, scope, pos, refusals, nextTemp)
				if msg != "" {
					return "", msg
				}
				hs = append(hs, h)
			default:
				return "", "JSX child is not lowerable"
			}
		}
	}
	arr := saAssembleHandles(w, hs, scope, nextTemp)
	for _, h := range hs {
		saConsumeOwn(scope, h)
	}
	box := saBoxJsValStd(w, scope, "ptr", 5, arr, nextTemp)
	saOwnTemp(scope, box)
	saConsumeOwn(scope, arr)
	return box, ""
}

// saLowerJsxElement lowers one JSX element / self-closing element /
// fragment to a jsval descriptor handle, or "" + a message.
func saLowerJsxElement(w printer.EmitTextWriter, e *ast.Node, scope *saScope, pos func(int) (int, int), refusals *[]SARefusal, nextTemp *int) (string, string) {
	if e == nil {
		return "", "missing JSX element"
	}
	var tag string
	var attrs *ast.Node
	var children *ast.NodeList
	switch e.Kind {
	case ast.KindJsxElement:
		je := e.AsJsxElement()
		if je == nil || je.OpeningElement == nil {
			return "", "JSX element has no opening element"
		}
		oe := je.OpeningElement.AsJsxOpeningElement()
		if oe == nil {
			return "", "JSX element has no opening element"
		}
		t, msg := saJsxTagName(oe.TagName)
		if msg != "" {
			return "", msg
		}
		tag = t
		attrs = oe.Attributes
		children = je.Children
	case ast.KindJsxSelfClosingElement:
		se := e.AsJsxSelfClosingElement()
		if se == nil {
			return "", "JSX element has no tag"
		}
		t, msg := saJsxTagName(se.TagName)
		if msg != "" {
			return "", msg
		}
		tag = t
		attrs = se.Attributes
		children = nil
	case ast.KindJsxFragment:
		jf := e.AsJsxFragment()
		if jf == nil {
			return "", "JSX fragment has no children"
		}
		tag = ""
		attrs = nil
		children = jf.Children
	default:
		return "", "expression is not a JSX element"
	}
	tagH := saLowerStringLiteral(w, tag, scope, nextTemp)
	props, msg := saJsxAttributes(w, attrs, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	kids, msg := saJsxChildren(w, children, scope, pos, refusals, nextTemp)
	if msg != "" {
		return "", msg
	}
	scope.addImport("sa_std/ts_jsx.sa")
	h := fmt.Sprintf("t_%d", *nextTemp)
	*nextTemp++
	w.Write(fmt.Sprintf("  %s = call @ts_jsx_element(%s, %s, %s)\n", h, tagH, props, kids))
	saOwnTemp(scope, h)
	saConsumeOwn(scope, tagH)
	saConsumeOwn(scope, props)
	saConsumeOwn(scope, kids)
	return h, ""
}
