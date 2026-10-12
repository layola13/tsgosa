interface Inner { v: number; w: number; }
interface Outer { p: Inner; q: number; }
function main(): i32 {
  const o: Outer = { p: { v: 1, w: 2 }, q: 3 };
  const { p: { v, ...rest }, q } = o;
  console.log(v + rest.w + q);
  return 0;
}
