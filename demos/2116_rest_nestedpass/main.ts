interface Inner { v: number; }
interface Outer { p: Inner; q: number; }
function main(): i32 {
  const o: Outer = { p: { v: 1 }, q: 2 };
  const { q, ...rest } = o;
  console.log(rest.p.v);
  return 0;
}
