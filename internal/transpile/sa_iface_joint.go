package transpile

import (
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// Phase P1: interface param→return joint fixpoint.
//
// R0's return analysis runs first, then P0's parameter analysis — but
// param prov was never fed back into return analysis, so `return c`
// where c is an interface param classified as unknown even when every
// caller passes the same concrete. The joint loop's only new mechanism
// is threading recorded param prov into R0's classifier as an entry
// source (saRetParamSeedEntries, class only, block bodies only),
// iterating {R0-fixpoint; P0-pass} until quiet. No new lowering,
// dispatch-site, attachment, or installation code: all four already
// consume whatever prov is recorded.
//
// Termination (design §1.5): prov fields only ever gain values, never
// change or lose them, so each non-quiet round records at least one new
// prov field; the field count is bounded by (interface-returning funcs
// + candidate params), plus one quiet round to observe no change.
// Breaking at the bound is fail-closed: already-recorded prov stays
// valid, only completeness is lost.
//
// Change detection compares prov CONTENT, not litFn agreement ids:
// P0's per-pass litSeq is fresh each pass, so the same litFn table can
// carry a different id across passes. Agreement is judged within one
// pass, where all ids come from one snapshot, so cross-pass id churn
// can only cause conservative refusal within a pass, never
// misdispatch — and must not keep the outer loop from observing quiet.
// Hence the snapshot records class strings and litFn nil-ness/length,
// never litIDs.

// saIfaceJointSnapshot encodes the prov fields of every signature into
// one deterministic string for change detection.
func saIfaceJointSnapshot(funcs map[string]saFuncSig) string {
	keys := make([]string, 0, len(funcs))
	for k := range funcs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		sig := funcs[k]
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(sig.retIfaceProv)
		b.WriteByte(0)
		if len(sig.retIfaceLitFn) > 0 {
			b.WriteString("L")
			b.WriteString(strings.Repeat("x", len(sig.retIfaceLitFn)))
		}
		b.WriteByte(0)
		for i := range sig.paramKinds {
			if i < len(sig.paramIfaceProv) {
				b.WriteString(sig.paramIfaceProv[i])
			}
			b.WriteByte(0)
			if i < len(sig.paramIfaceLitFn) && sig.paramIfaceLitFn[i] != nil {
				b.WriteString("L")
				b.WriteString(strings.Repeat("x", len(sig.paramIfaceLitFn[i])))
			}
			b.WriteByte(0)
		}
		b.WriteByte(1)
	}
	return b.String()
}

// saAnalyzeIfaceJointProv runs R0's return analysis, P0's parameter
// analysis, and the Phase F field analysis to a joint fixpoint, in the
// same position (before the main lowering loop) where the sequential
// hooks ran. Each round runs R0's analysis to its own fixpoint, then
// P0's pass, then the field pass, in that order. It mutates only the
// funcs map (prov fields) and saClassDef.fieldProv, and adds no
// refusals (fail-closed).
func saAnalyzeIfaceJointProv(sf *ast.SourceFile, funcs map[string]saFuncSig, classes map[string]*saClassDef, aliasOf map[string]*ast.TypeNode, link *saFileLink) {
	if sf == nil || funcs == nil {
		return
	}
	bound := 1
	for _, sig := range funcs {
		bound++ // one return-prov field slot per function (conservative)
		bound += len(sig.paramKinds)
	}
	for _, def := range classes {
		if def != nil {
			bound += len(def.fields) // one field-prov slot per field (conservative)
		}
	}
	for round := 0; round <= bound; round++ {
		before := saIfaceJointSnapshot(funcs)
		beforeFields := saIfaceFieldSnapshot(classes)
		saAnalyzeReturnIfaceProv(sf, funcs, classes, aliasOf)
		saAnalyzeParamsIfaceProv(sf, funcs, classes, aliasOf, link)
		saAnalyzeFieldIfaceProv(sf, funcs, classes, aliasOf, link)
		if saIfaceJointSnapshot(funcs) == before && saIfaceFieldSnapshot(classes) == beforeFields {
			break
		}
	}
}
