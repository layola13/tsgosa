const A = [1, 2];
function g(): i32[] {
  return A;
}
function h(): i32 {
  const B = A;
  return B[0];
}
function main(): i32 {
  return g()[0] + h();
}
