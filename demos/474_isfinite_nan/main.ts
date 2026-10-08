function clamp(parsed: f64, fallback: f64): f64 {
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback;
}
function main(): i32 {
  console.log(clamp(2.5, 9));
  console.log(clamp(-1, 9));
  const inf: f64 = 1e308 + 1e308;
  console.log(Number.isFinite(inf));
  console.log(Number.isNaN(inf));
  console.log(Number.isNaN(1.5));
  return 0;
}
