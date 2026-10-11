class C { n: i32 = 0; m: i32 = 0; constructor(a: i32[]) { this.n = a[0]; this.m = a[0] + a[1]; } }
function main(): i32 {
  const c = new C([3, 4]);
  console.log(c.n + c.m);
  return 0;
}
