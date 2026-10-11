class C { n: i32 = 0; constructor(k: i32) { this.n = (k + 1) * 2; } }
function main(): i32 {
  const c = new C(20);
  console.log(c.n);
  return 0;
}
