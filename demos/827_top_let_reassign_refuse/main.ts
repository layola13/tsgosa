let R = [1, 2];
function m(): i32 {
  R = [9, 9];
  return R[0];
}
function s(): i32 {
  R[0] = 7;
  return 0;
}
function main(): i32 {
  return m() + s();
}
