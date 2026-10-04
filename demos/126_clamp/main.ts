function clamp(x: i32, lo: i32, hi: i32): i32 {
  return Math.min(Math.max(x, lo), hi);
}
function main(): i32 {
  console.log(clamp(5, 1, 10), clamp(-3, 1, 10), clamp(99, 1, 10));
  return 0;
}
