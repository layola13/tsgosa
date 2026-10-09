const s = "hi";
if (s ?? "") {
  console.log(1);
} else {
  console.log(0);
}
function f(x: string | null): i32 {
  let n = 0;
  do {
    n = n + 1;
  } while (n < 2 && (x ?? "d"));
  return n;
}
console.log(f(null));
console.log(f(""));
