class Inner { v: number = 0; }
interface Outer { p: Inner; }
class C {
  n: number = 0;
  constructor(oo: Outer) { this.n = oo.p.v; }
}
function main(): i32 {
  const inner = new Inner();
  inner.v = 7;
  const o: Outer = { p: inner };
  const c = new C(o);
  console.log(c.n);
  return 0;
}
