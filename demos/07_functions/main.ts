function fact(n: i32): i32 {
  if (n <= 1) {
    return 1;
  }
  return n * fact(n - 1);
}
function add(a: i32, b: i32): i32 {
  return a + b;
}
function main(): i32 {
  console.log(fact(5));
  console.log(add(40, 2));
  return 0;
}
