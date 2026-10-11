interface P { a: f64; b: f64; }
function main(): i32 {
  const p: P = { a: 1.5, b: 2.5 };
  console.log(JSON.stringify(p));
  return 0;
}
