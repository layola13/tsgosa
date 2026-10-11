class C { s: string = ""; constructor(t: string) { this.s = t[t.length - 1]; } }
function main(): i32 {
  const c = new C("hey");
  console.log(c.s);
  return 0;
}
