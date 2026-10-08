function main(): i32 {
  const f = function fact(n: i32): i32 {
    return n <= 1 ? 1 : n * fact(n - 1);
  };
  console.log(f(5));
  return 0;
}
