class C {
  rate: number = 0;
  constructor(r: number) { this.rate = r; }
}
function main(): i32 {
  const c = new C(7);
  console.log(c.rate);
  return 0;
}
