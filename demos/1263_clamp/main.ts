function clamp(v: i32, lo: i32, hi: i32): i32 {
  if (v < lo) { return lo; }
  if (v > hi) { return hi; }
  return v;
}
function main(): i32 {
  console.log(clamp(5, 0, 10));
  console.log(clamp(-5, 0, 10));
  console.log(clamp(50, 0, 10));
  return 0;
}
