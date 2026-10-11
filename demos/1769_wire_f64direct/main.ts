class C { r: f64 = 0; constructor(p: f64) { this.r = p; } }
function main(): i32 {
  const c = new C(2.5);
  console.log(c.r);
  return 0;
}
