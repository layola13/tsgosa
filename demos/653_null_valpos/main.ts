function f(): void {
  const s = "hi";
  const b = s == null ? 1 : 0;
  console.log(b);
  console.log(s != null ? 3 : 4);
}
f();
