function g(ss: string[][]): string {
  const [x = []] = ss;
  return x[0];
}
function main(): i32 {
  console.log(g([["xy"], ["z"]]));
  return 0;
}
