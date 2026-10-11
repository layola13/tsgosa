class C { n: i32 = 0; m: i32 = 0; constructor(t: string) { this.n = t.length; this.m = t.length * 2; } }
function main(): i32 {
  const c = new C("hey");
  console.log(c.n + c.m);
  return 0;
}
