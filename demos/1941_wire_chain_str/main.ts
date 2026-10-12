interface Inner { s: string; }
interface Outer { p: Inner; }
class C {
  t: string = "";
  constructor(oo: Outer) { this.t = oo.p.s; }
}
function main(): i32 {
  const inner: Inner = { s: "hi" };
  const o: Outer = { p: inner };
  const c = new C(o);
  console.log(c.t);
  return 0;
}
