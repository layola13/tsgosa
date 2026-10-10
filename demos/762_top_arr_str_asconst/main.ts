const X = ["x", "yy"] as const;
function main(): i32 {
  console.log(X[0]);
  console.log(X.length + X[1].length);
  return 0;
}
