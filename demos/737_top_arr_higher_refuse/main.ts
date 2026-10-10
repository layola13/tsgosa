const A = [1, 2, 3];
function add(a: i32, b: i32): i32 {
  return a + b;
}
function s(): i32 {
  return add(...A);
}
function m(): i32 {
  A.map((x) => x * 2);
  return 0;
}
function main(): i32 {
  return s() + m();
}
