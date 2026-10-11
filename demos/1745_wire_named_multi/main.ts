class F { n: i32 = 0; m: i32 = 0; constructor(k) { this.n = k + 1; this.m = k * 10; } }
function main(): i32 {
  const f = new F(8);
  console.log(f.n);
  console.log(f.m);
  return 0;
}
