class C { n: i32 = 0; constructor(t: string) { this.n = t.length; } }
function main(): i32 {
  const c = new C("hey");
  console.log(c.n);
  return 0;
}
