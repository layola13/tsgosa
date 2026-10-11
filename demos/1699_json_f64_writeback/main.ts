interface F { r: f64; n: i32; }
function main(): i32 {
  const f: F = { r: 1.5, n: 1 };
  f.r = 7.5;
  console.log(JSON.stringify(f));
  return 0;
}
