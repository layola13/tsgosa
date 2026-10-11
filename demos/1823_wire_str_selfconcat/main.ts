class C { s: string = ""; constructor(t: string) { this.s = t + t; } }
function main(): i32 {
  const c = new C("ab");
  console.log(c.s);
  return 0;
}
