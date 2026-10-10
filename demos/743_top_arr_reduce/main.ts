const A = [1, 2, 3, 4];
function main(): i32 {
  console.log(A.reduce((s, x) => s + x, 0));
  console.log(A.reduceRight((s, x) => s - x, 100));
  return 0;
}
