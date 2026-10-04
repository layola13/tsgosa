function gcd(a: i32, b: i32): i32 {
  if (b == 0) {
    return a;
  }
  return gcd(b, a % b);
}
function lcm(a: i32, b: i32): i32 {
  return (a / gcd(a, b)) * b;
}
function main(): i32 {
  console.log(lcm(4, 6), lcm(7, 5));
  return 0;
}