const A = [1, 2, 3];
function add(a: i32, b: i32): i32 {
  return a + b;
}
function sumAll(...ns: i32[]): i32 {
  return ns[0] + ns.length;
}
function s(): i32 {
  return add(...A);
}
function m(): i32 {
  return sumAll(...A);
}
function main(): i32 {
  return s() + m();
}
