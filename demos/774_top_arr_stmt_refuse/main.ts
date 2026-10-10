const A = [1, 2];
function pi(): i32 {
  ++A[0];
  return 0;
}
function dl(): i32 {
  delete A[0];
  return 0;
}
function ls(): i32 {
  A.length = 5;
  return 0;
}
function main(): i32 {
  return pi() + dl() + ls();
}
