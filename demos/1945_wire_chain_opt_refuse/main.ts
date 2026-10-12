interface Inner { v: number; }
interface Outer { p: Inner|null; }
class C {
  n: number = 0;
  constructor(oo: Outer) { this.n = oo.p?.v ?? -1; }
}
function main(): i32 {
  const inner: Inner = { v: 7 };
  const o: Outer = { p: inner };
  const c = new C(o);
  console.log(c.n);
  return 0;
}
