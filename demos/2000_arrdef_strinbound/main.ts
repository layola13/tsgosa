function g(ss: string[][]): string {
  const [x = ["a"]] = ss;
  return x[0];
}
function main(): i32 {
  console.log(g([["x"], ["y"]]));
  return 0;
}
