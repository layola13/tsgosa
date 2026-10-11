function get(aa: i32[][]): i32 {
  const [[, a]] = aa;
  return a;
}
function main(): i32 {
  const aa: i32[][] = [[9, 8]];
  console.log(get(aa));
  return 0;
}
