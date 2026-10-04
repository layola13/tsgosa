function gcd(a: i32, b: i32): i32 {
  while (b != 0) {
    const t: i32 = a % b;
    a = b;
    b = t;
  }
  return a;
}
function main(): i32 {
  console.log(gcd(12, 8), gcd(100, 75), gcd(17, 5));
  return 0;
}