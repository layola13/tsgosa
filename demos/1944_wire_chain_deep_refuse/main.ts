interface L3 { v: number; }
interface L2 { inner: L3; }
interface L1 { mid: L2; }
class C {
  n: number = 0;
  constructor(a: L1) { this.n = a.mid.inner.v; }
}
function main(): i32 {
  const l3: L3 = { v: 7 };
  const l2: L2 = { inner: l3 };
  const l1: L1 = { mid: l2 };
  const c = new C(l1);
  console.log(c.n);
  return 0;
}
