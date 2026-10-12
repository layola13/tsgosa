interface Inner { v: number; }
interface Outer { p: Inner; }
class C {
  n: number = 0;
  constructor(o: Outer) { this.n = o.p.v; }
}
function main(): i32 {
  const inner: Inner = { v: 7 };
  const o: Outer = { p: inner };
  const c = new C(o);
  console.log(c.n);
  return 0;
}
