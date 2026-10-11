function get(ss: string[][]): string {
  const [[s]] = ss;
  return s;
}
function main(): i32 {
  const ss: string[][] = [["x"]];
  console.log(get(ss));
  return 0;
}
