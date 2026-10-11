interface F { r: f64; }
function main(): i32 {
  const f: F = { r: 2.5 };
  console.log(JSON.stringify(f));
  return 0;
}
