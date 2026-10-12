interface Inner { v: number; }
interface Outer { p: Inner; }
function get(oo: Outer): number {
  return oo.p.v;
}
function main(): i32 {
  const inner: Inner = { v: 7 };
  const o: Outer = { p: inner };
  console.log(get(o));
  return 0;
}
