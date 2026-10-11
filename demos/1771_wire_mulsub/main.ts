class C { n: i32 = 0; constructor(k: i32) { this.n = k * 3 - 1; } }
function main(): i32 {
  const c = new C(14);
  console.log(c.n);
  return 0;
}
