class C { n: i32 = 0; constructor(k: i32) { this.n = k == 3 ? 7 : 8; } }
function main(): i32 {
  const c = new C(3);
  console.log(c.n);
  return 0;
}
