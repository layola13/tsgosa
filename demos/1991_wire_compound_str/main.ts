class C {
  s: string = "a";
  constructor(t: string) { this.s += t; }
}
function main(): i32 {
  const c = new C("b");
  console.log(c.s);
  return 0;
}
