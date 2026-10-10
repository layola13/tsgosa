const A = [1, 2];
function st(): i32 {
  const s = new Set(A);
  return 0;
}
function mp(): i32 {
  const m = new Map(A);
  return 0;
}
function main(): i32 {
  return st() + mp();
}
