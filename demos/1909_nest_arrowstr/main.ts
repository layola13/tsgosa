const f = (ss: string[][]): string => {
  const [[s]] = ss;
  return s;
};
function main(): i32 {
  const ss: string[][] = [["x"]];
  console.log(f(ss));
  return 0;
}
