interface M { a: number; r: f64; }
function main(): i32 {
  const o: M = { a: 1, r: 2.5 };
  const { a, ...rest } = o;
  console.log(rest.r);
  return 0;
}
