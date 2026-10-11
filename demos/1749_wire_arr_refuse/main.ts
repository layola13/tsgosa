class F { n: i32 = 0; constructor(a: i32[]) { this.n = a[0] + 1; } }
function main(): i32 {
  const f = new F([7, 8]);
  console.log(f.n);
  return 0;
}
