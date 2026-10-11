class C { s: string = ""; r: string = ""; constructor(t: string) { this.s = t[0]; this.r = t[1]; } }
function main(): i32 {
  const c = new C("hey");
  console.log(c.s + c.r);
  return 0;
}
