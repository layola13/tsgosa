interface Q { s: string; n: number; }
function main(): i32 {
  const o: Q = { s: "hi", n: 3 };
  const { n, ...rest } = o;
  console.log(rest.s);
  console.log(n);
  return 0;
}
