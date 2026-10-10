const D = [[[1, 2]], [[3]]];
function f(): i32 {
  const D = [[[9]]];
  return D[0][0][0];
}
function main(): i32 {
  console.log(f());
  console.log(D[1][0][0]);
  return 0;
}
