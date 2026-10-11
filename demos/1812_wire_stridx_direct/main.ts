class C { s: string = ""; r: string = ""; constructor(t: string) { this.s = t; this.r = t[0]; } }
function main(): i32 {
  const c = new C("hey");
  console.log(c.s);
  console.log(c.r);
  return 0;
}
