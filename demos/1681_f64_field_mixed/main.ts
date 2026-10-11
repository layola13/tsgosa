class A { rate: f64 = 2.5; n: i32 = 7; s: string = "hi"; flag: boolean = true; }
function main(): i32 {
  const a = new A();
  console.log(a.rate);
  console.log(a.n);
  console.log(a.s);
  console.log(a.flag ? 1 : 0);
  return 0;
}
