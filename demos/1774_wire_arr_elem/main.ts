class C { n: i32 = 0; constructor(a: i32[]) { this.n = a[0]; } }
function main(): i32 {
  const c = new C([8, 9]);
  console.log(c.n);
  return 0;
}
