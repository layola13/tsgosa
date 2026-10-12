function g(ss: string[][]): string {
  const [x = ["a"], y = ["b"]] = ss;
  return x[0] + y[0];
}
function main(): i32 {
  console.log(g([["x"], ["y"]]));
  console.log(g([["x"]]));
  return 0;
}
