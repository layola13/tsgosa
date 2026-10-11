const f = (aa: i32[][]): i32 => {
  const [[a]] = aa;
  return a;
};
function main(): i32 {
  const aa: i32[][] = [[1]];
  console.log(f(aa));
  return 0;
}
