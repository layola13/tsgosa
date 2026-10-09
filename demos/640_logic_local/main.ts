function f(): void {
  const s = "hi";
  const e = "";
  if (s && e) {
    console.log(1);
  } else {
    console.log(0);
  }
  if (e || s) {
    console.log(3);
  } else {
    console.log(4);
  }
}
f();
