class C { s: string = ""; constructor(t: string, k: i32) { this.s = t[k]; } }
function main(): i32 {
  const c = new C("hey", 1);
  console.log(c.s);
  return 0;
}
