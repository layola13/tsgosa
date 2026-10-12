interface Inner { v: number; }
interface Outer { p: Inner; }
class C {
  a: number = 0;
  b: number = 0;
  constructor(oo: Outer) { this.a = oo.p.v; this.b = oo.p.v; }
}
function main(): i32 {
  const inner: Inner = { v: 7 };
  const o: Outer = { p: inner };
  const c = new C(o);
  console.log(c.a);
  console.log(c.b);
  return 0;
}
