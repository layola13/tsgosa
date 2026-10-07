function main(): i32 {
  const f = function g(n: i32): i32 {
    if (n <= 1) {
      return 1;
    }
    return g(n - 1);
  };
  console.log(f(3));
  return 0;
}
