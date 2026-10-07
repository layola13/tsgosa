function main(): i32 {
  console.log(isNaN(5));
  console.log(isFinite(5));
  console.log(Number.isNaN(5));
  console.log(Number.isFinite(5));
  console.log(Number.isNaN("x"));
  return 0;
}
