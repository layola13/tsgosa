function gcd(a: i32, b: i32): i32 {
  while (b != 0) {
    const t: i32 = a % b;
    a = b;
    b = t;
  }
  return a;
}
function main(): i32 {
  let t: i32 = 0;
  for (let i: i32 = 1; i <= 10; i++) {
    t = t + gcd(i, 12);
  }
  console.log(t);
  return 0;
}