const A = [1, 2];
function p(): i32 {
  A.push(3);
  return 0;
}
function q(): i32 {
  A[0] = 9;
  return A[0];
}
function main(): i32 {
  return p() + q();
}
