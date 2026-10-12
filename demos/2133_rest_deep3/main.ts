interface L3 { a: number; b: number; }
interface L2 { p: L3; q: number; }
interface L1 { o: L2; r: number; }
function main(): i32 {
  const v: L1 = { o: { p: { a: 1, b: 2 }, q: 3 }, r: 4 };
  const { r, ...rest } = v;
  console.log(rest.o.p.a + rest.o.p.b + rest.o.q + r);
  return 0;
}
