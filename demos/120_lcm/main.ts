function gcd(a: i32, b: i32): i32 {
  if (b == 0) {
    return a;
  }
  return gcd(b, a % b);
}
function main(): i32 {
  const g: i32 = gcd(48, 18);
  console.log(g, (48 / g) * 18);
  return 0;
}
