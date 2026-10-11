interface M { a: f64; b: f64; s: string; n: i32; }
function main(): i32 {
  const m: M = { a: 1.5, b: 2.5, s: "k", n: 9 };
  console.log(m.a + m.b);
  console.log(m.s);
  console.log(m.n);
  return 0;
}
