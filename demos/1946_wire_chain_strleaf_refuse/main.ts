interface Inner { s: string; }
interface Outer { p: Inner; }
class C {
  n: number = 0;
  constructor(oo: Outer) { this.n = oo.p.s; }
}
function main(): i32 {
  const inner: Inner = { s: "hi" };
  const o: Outer = { p: inner };
  const c = new C(o);
  console.log(c.n);
  return 0;
}
