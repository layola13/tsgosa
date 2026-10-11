interface S { s: string; r: f64; }
function main(): i32 {
  const o: S = { s: "hi", r: 2.5 };
  console.log(JSON.stringify(o));
  return 0;
}
