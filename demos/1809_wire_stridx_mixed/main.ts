class C { s: string = ""; n: i32 = 0; constructor(t: string) { this.s = t[0]; this.n = t.length; } }
function main(): i32 {
  const c = new C("hey");
  console.log(c.s);
  console.log(c.n);
  return 0;
}
