function fib(n: i32): i32 {
  if (n <= 1) { return n; }
  return fib(n - 1) + fib(n - 2);
}
function main(): i32 {
  console.log(fib(10));
  console.log(fib(8));
  return 0;
}
