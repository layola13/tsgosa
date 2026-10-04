function fib(n: i32): i32 {
  if (n <= 1) {
    return n;
  }
  return fib(n - 1) + fib(n - 2);
}
function main(): i32 {
  const a: number[] = [5, 2, 8, 1];
  a.sort();
  console.log(fib(10), a[0] + a[3]);
  return 0;
}