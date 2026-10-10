const N = [[1, 2], [3, 4]];
function f(): i32 {
  const N = [[9]];
  return N[0][0];
}
function main(): i32 {
  console.log(f());
  console.log(N[1][1]);
  return 0;
}
