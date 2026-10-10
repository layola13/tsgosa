const A = [1, 2];
function oc(): i32 {
  console.log(A?.slice(1).length);
  return 0;
}
function bm(): i32 {
  const f = A.slice;
  return 0;
}
function main(): i32 {
  return oc() + bm();
}
