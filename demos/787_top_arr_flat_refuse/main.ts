const A = [1, 2, 3];
function dbl(x: i32): i32[] {
  return [x, x];
}
function m(): i32 {
  A.flatMap(dbl);
  return 0;
}
function f(): i32 {
  A.fill(0);
  return 0;
}
function main(): i32 {
  return m() + f();
}
