const Q = [[[[1]]]];
function single(): i32 {
  console.log(Q[0]);
  return 0;
}
function quad(): i32 {
  console.log(Q[0][0][0][0]);
  return 0;
}
function main(): i32 {
  return single() + quad();
}
