function isPrime(n: i32): i32 {
  if (n < 2) {
    return 0;
  }
  for (let i: i32 = 2; i * i <= n; i++) {
    if (n % i == 0) {
      return 0;
    }
  }
  return 1;
}
function main(): i32 {
  let c: i32 = 0;
  for (let i: i32 = 2; i < 30; i++) {
    c = c + isPrime(i);
  }
  console.log(c);
  return 0;
}