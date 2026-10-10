const A = [1, 2, 3];
function dbl(x: i32): i32 {
  return x * 2;
}
function m(): i32 {
  A.map(dbl);
  return 0;
}
function n(): i32 {
  const z = A.filter(dbl);
  return z[0];
}
function main(): i32 {
  return m() + n();
}
